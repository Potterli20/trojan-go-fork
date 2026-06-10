package transport

import (
	"context"
	"os"
	"os/exec"
	"strconv"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/tunnel"
	"github.com/Potterli20/trojan-go-fork/tunnel/freedom"
)

// Client implements tunnel.Client
type Client struct {
	serverAddress *tunnel.Address
	cmd           *exec.Cmd
	ctx           context.Context
	cancel        context.CancelFunc
	direct        *freedom.Client
}

func (c *Client) Close() error {
	log.Info("[Transport] Closing client")
	c.cancel()
	if c.cmd != nil && c.cmd.Process != nil {
		if log.ShouldLog(log.DebugLevel) {
			log.Debug("[Transport] Killing transport plugin process")
		}
		if err := c.cmd.Process.Kill(); err != nil {
			log.Error("[Transport] Failed to kill plugin process:", err)
			return err
		}
		if log.ShouldLog(log.DebugLevel) {
			log.Debug("[Transport] Waiting for plugin process to exit")
		}
		c.cmd.Wait()
		log.Info("[Transport] Transport plugin process killed")
	}
	log.Info("[Transport] Client closed successfully")
	return nil
}

func (c *Client) DialPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	log.Warn("[Transport] DialPacket is not supported")
	panic("not supported")
}

// DialConn implements tunnel.Client. It will ignore the params and directly dial to the remote server
func (c *Client) DialConn(*tunnel.Address, tunnel.Tunnel) (tunnel.Conn, error) {
	if log.ShouldLog(log.DebugLevel) {
		log.Debug("[Transport] DialConn start - target:", c.serverAddress.String())
	}

	tracker := log.NewConnectionTracker("Transport", "DialConn").
		WithField("target", c.serverAddress.String())

	conn, err := c.direct.DialConn(c.serverAddress, nil)
	if err != nil {
		tracker.Error(err)
		return nil, common.NewError("transport failed to connect to remote server").Base(err)
	}
	tracker.Success()

	return &Conn{
		Conn: conn,
	}, nil
}

// startPlugin starts the transport plugin command
func startPlugin(cmd *exec.Cmd) error {
	if log.ShouldLog(log.DebugLevel) {
		log.Debug("[Transport] Starting plugin:", cmd.Path, cmd.Args)
	}
	if err := cmd.Start(); err != nil {
		log.Error("[Transport] Failed to start plugin:", err)
		return common.NewError("failed to start transport plugin").Base(err)
	}
	log.Info("[Transport] Plugin started successfully, PID:", cmd.Process.Pid)
	return nil
}

// NewClient creates a transport layer client
func NewClient(ctx context.Context, _ tunnel.Client) (*Client, error) {
	cfg := config.FromContext(ctx, Name).(*Config)

	log.Info("[Transport] Creating client")
	if log.ShouldLog(log.DebugLevel) {
		log.Debug("[Transport] RemoteHost:", cfg.RemoteHost)
		log.Debug("[Transport] RemotePort:", cfg.RemotePort)
		log.Debug("[Transport] TransportPlugin Enabled:", cfg.TransportPlugin.Enabled)
	}

	var cmd *exec.Cmd
	serverAddress := tunnel.NewAddressFromHostPort("tcp", cfg.RemoteHost, cfg.RemotePort)
	if log.ShouldLog(log.DebugLevel) {
		log.Debug("[Transport] Server address:", serverAddress.String())
	}

	if cfg.TransportPlugin.Enabled {
		log.Warn("[Transport] Using transport plugin - working in plain text mode")
		if log.ShouldLog(log.DebugLevel) {
			log.Debug("[Transport] Plugin type:", cfg.TransportPlugin.Type)
			log.Debug("[Transport] Plugin command:", cfg.TransportPlugin.Command)
			log.Debug("[Transport] Plugin args:", cfg.TransportPlugin.Arg)
		}

		switch cfg.TransportPlugin.Type {
		case "shadowsocks":
			if log.ShouldLog(log.DebugLevel) {
				log.Debug("[Transport] Configuring Shadowsocks plugin...")
			}
			pluginHost := "127.0.0.1"
			pluginPort := common.PickPort("tcp", pluginHost)
			cfg.TransportPlugin.Env = append(
				cfg.TransportPlugin.Env,
				"SS_LOCAL_HOST="+pluginHost,
				"SS_LOCAL_PORT="+strconv.FormatInt(int64(pluginPort), 10),
				"SS_REMOTE_HOST="+cfg.RemoteHost,
				"SS_REMOTE_PORT="+strconv.FormatInt(int64(cfg.RemotePort), 10),
				"SS_PLUGIN_OPTIONS="+cfg.TransportPlugin.Option,
			)
			cfg.RemoteHost = pluginHost
			cfg.RemotePort = pluginPort
			serverAddress = tunnel.NewAddressFromHostPort("tcp", cfg.RemoteHost, cfg.RemotePort)
			if log.ShouldLog(log.DebugLevel) {
				log.Debug("[Transport] Plugin local address:", serverAddress.String())
				log.Debug("[Transport] Plugin environment variables:", cfg.TransportPlugin.Env)
			}

			cmd = exec.Command(cfg.TransportPlugin.Command, cfg.TransportPlugin.Arg...)
			cmd.Env = append(cmd.Env, cfg.TransportPlugin.Env...)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stdout
			if err := startPlugin(cmd); err != nil {
				return nil, err
			}
		case "other":
			if log.ShouldLog(log.DebugLevel) {
				log.Debug("[Transport] Configuring custom plugin...")
			}
			cmd = exec.Command(cfg.TransportPlugin.Command, cfg.TransportPlugin.Arg...)
			cmd.Env = append(cmd.Env, cfg.TransportPlugin.Env...)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stdout
			if err := startPlugin(cmd); err != nil {
				return nil, err
			}
		case "plaintext":
			if log.ShouldLog(log.DebugLevel) {
				log.Debug("[Transport] Using plaintext mode - no plugin")
			}
		default:
			log.Error("[Transport] Invalid plugin type:", cfg.TransportPlugin.Type)
			return nil, common.NewError("invalid plugin type: " + cfg.TransportPlugin.Type)
		}
	} else if log.ShouldLog(log.DebugLevel) {
		log.Debug("[Transport] No transport plugin - direct connection")
	}

	if log.ShouldLog(log.DebugLevel) {
		log.Debug("[Transport] Creating freedom client...")
	}
	direct, err := freedom.NewClient(ctx, nil)
	if err != nil {
		log.Error("[Transport] Failed to create freedom client:", err)
		if cmd != nil && cmd.Process != nil {
			if log.ShouldLog(log.DebugLevel) {
				log.Debug("[Transport] Killing plugin process due to error")
			}
			cmd.Process.Kill()
			cmd.Wait()
		}
		return nil, err
	}
	log.Info("[Transport] Freedom client created successfully")

	ctx, cancel := context.WithCancel(ctx)
	client := &Client{
		serverAddress: serverAddress,
		cmd:           cmd,
		ctx:           ctx,
		cancel:        cancel,
		direct:        direct,
	}

	log.Info("[Transport] Client created successfully")
	return client, nil
}
