package transport

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"time"

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
	log.Info("[Transport Client] Closing transport client")
	c.cancel()
	if c.cmd != nil && c.cmd.Process != nil {
		log.Debug("[Transport Client] Killing transport plugin process")
		if err := c.cmd.Process.Kill(); err != nil {
			log.Error("[Transport Client] Failed to kill plugin process:", err)
			return err
		}
		log.Info("[Transport Client] Transport plugin process killed")
	}
	log.Info("[Transport Client] Transport client closed successfully")
	return nil
}

func (c *Client) DialPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	log.Warn("[Transport Client] DialPacket is not supported")
	panic("not supported")
}

// DialConn implements tunnel.Client. It will ignore the params and directly dial to the remote server
func (c *Client) DialConn(*tunnel.Address, tunnel.Tunnel) (tunnel.Conn, error) {
	log.Debug("[Transport Client] ========== Transport DialConn Start ==========")
	log.Debug("[Transport Client] Target server:", c.serverAddress.String())

	log.Debug("[Transport Client] Step 1: Dialing to remote server...")
	startTime := time.Now()
	conn, err := c.direct.DialConn(c.serverAddress, nil)
	dialDuration := time.Since(startTime)
	if err != nil {
		log.Error("[Transport Client] Failed to connect to remote server after", dialDuration, ":", err)
		return nil, common.NewError("transport failed to connect to remote server").Base(err)
	}
	log.Info("[Transport Client] Connection to remote server established in", dialDuration)
	log.Debug("[Transport Client] ========== Transport DialConn End ==========")

	return &Conn{
		Conn: conn,
	}, nil
}

// startPlugin starts the transport plugin command
func startPlugin(cmd *exec.Cmd) error {
	log.Debug("[Transport Client] Starting transport plugin:", cmd.Path, cmd.Args)
	if err := cmd.Start(); err != nil {
		log.Error("[Transport Client] Failed to start transport plugin:", err)
		return common.NewError("failed to start transport plugin").Base(err)
	}
	log.Info("[Transport Client] Transport plugin started successfully, PID:", cmd.Process.Pid)
	return nil
}

// NewClient creates a transport layer client
func NewClient(ctx context.Context, _ tunnel.Client) (*Client, error) {
	cfg := config.FromContext(ctx, Name).(*Config)

	log.Info("[Transport Client] ========== Creating Transport Client ==========")
	log.Debug("[Transport Client] RemoteHost:", cfg.RemoteHost)
	log.Debug("[Transport Client] RemotePort:", cfg.RemotePort)
	log.Debug("[Transport Client] TransportPlugin Enabled:", cfg.TransportPlugin.Enabled)

	var cmd *exec.Cmd
	serverAddress := tunnel.NewAddressFromHostPort("tcp", cfg.RemoteHost, cfg.RemotePort)
	log.Debug("[Transport Client] Server address:", serverAddress.String())

	if cfg.TransportPlugin.Enabled {
		log.Warn("[Transport Client] Using transport plugin - working in plain text mode")
		log.Debug("[Transport Client] Plugin type:", cfg.TransportPlugin.Type)
		log.Debug("[Transport Client] Plugin command:", cfg.TransportPlugin.Command)
		log.Debug("[Transport Client] Plugin args:", cfg.TransportPlugin.Arg)

		switch cfg.TransportPlugin.Type {
		case "shadowsocks":
			log.Debug("[Transport Client] Configuring Shadowsocks plugin...")
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
			log.Debug("[Transport Client] Plugin local address:", serverAddress.String())
			log.Debug("[Transport Client] Plugin environment variables:", cfg.TransportPlugin.Env)

			cmd = exec.Command(cfg.TransportPlugin.Command, cfg.TransportPlugin.Arg...)
			cmd.Env = append(cmd.Env, cfg.TransportPlugin.Env...)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stdout
			if err := startPlugin(cmd); err != nil {
				return nil, err
			}
		case "other":
			log.Debug("[Transport Client] Configuring custom plugin...")
			cmd = exec.Command(cfg.TransportPlugin.Command, cfg.TransportPlugin.Arg...)
			cmd.Env = append(cmd.Env, cfg.TransportPlugin.Env...)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stdout
			if err := startPlugin(cmd); err != nil {
				return nil, err
			}
		case "plaintext":
			log.Debug("[Transport Client] Using plaintext mode - no plugin")
		default:
			log.Error("[Transport Client] Invalid plugin type:", cfg.TransportPlugin.Type)
			return nil, common.NewError("invalid plugin type: " + cfg.TransportPlugin.Type)
		}
	} else {
		log.Debug("[Transport Client] No transport plugin - direct connection")
	}

	log.Debug("[Transport Client] Creating freedom client...")
	direct, err := freedom.NewClient(ctx, nil)
	if err != nil {
		log.Error("[Transport Client] Failed to create freedom client:", err)
		return nil, err
	}
	log.Info("[Transport Client] Freedom client created successfully")

	ctx, cancel := context.WithCancel(ctx)
	client := &Client{
		serverAddress: serverAddress,
		cmd:           cmd,
		ctx:           ctx,
		cancel:        cancel,
		direct:        direct,
	}

	log.Info("[Transport Client] ========== Transport Client Created Successfully ==========")
	return client, nil
}
