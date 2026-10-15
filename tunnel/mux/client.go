package mux

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/xtaci/smux"

	"github.com/Potterli20/trojan-go-fork/common"
	"github.com/Potterli20/trojan-go-fork/config"
	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/Potterli20/trojan-go-fork/tunnel"
)

type muxID uint64

func generateMuxID() muxID {
	id, err := common.SecureRandUint32()
	if err != nil {
		// crypto/rand 失败极罕见，降级为时间戳保证不阻塞连接建立
		log.Warn("failed to generate secure mux ID, falling back to timestamp:", err)
		return muxID(uint32(time.Now().UnixNano())) //gosec:disable -- 降级路径：截断为 uint32 仅用于避免 panic，不要求精确
	}
	return muxID(id)
}

type smuxClientInfo struct {
	id             muxID
	client         *smux.Session
	lastActiveTime atomic.Int64
	underlayConn   tunnel.Conn
}

// Client is a smux client
type Client struct {
	clientPoolLock sync.Mutex
	clientPool     map[muxID]*smuxClientInfo
	underlay       tunnel.Client
	concurrency    int
	timeout        time.Duration
	ctx            context.Context
	cancel         context.CancelFunc
	wg             sync.WaitGroup
}

func (c *Client) Close() error {
	c.cancel()
	c.wg.Wait()
	// 会话关闭可能因底层写阻塞而耗时，必须在锁外执行，
	// 避免持有 clientPoolLock 拖死并发的 DialConn
	c.clientPoolLock.Lock()
	pending := make([]*smuxClientInfo, 0, len(c.clientPool))
	for id, info := range c.clientPool {
		pending = append(pending, info)
		delete(c.clientPool, id)
		log.Debug("mux client", id, "closed")
	}
	c.clientPoolLock.Unlock()
	for _, info := range pending {
		info.client.Close()       //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
		info.underlayConn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
	}
	return nil
}

func (c *Client) cleanLoop() {
	// 空闲时限与巡检间隔必须来自同一个值。旧代码只给间隔做了兜底
	// （timeout<=0 时 interval=10s），回收判据却仍拿 c.timeout 原始值比较：
	// `NumStreams()==0 && 空闲 > 0` 恒真，于是每个空闲会话在第一次巡检就被拆掉，
	// 会话复用退化成了"每条新连接重开一个 mux 会话"。
	// 非正数（idle-timeout: 0 或负）按配置失误处理，落到文档默认值。
	idleTimeout := c.timeout
	if idleTimeout <= 0 {
		log.Warn("mux idle-timeout is not positive, using ", idleDefaultTimeout)
		idleTimeout = idleDefaultTimeout
	}
	checkDuration := idleTimeout / 4
	log.Debug("check duration:", checkDuration.Seconds(), "s")
	ticker := time.NewTicker(checkDuration)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			// 与 Close 同理：会话关闭（含 stickyConn 写 padding）可能阻塞，
			// 锁内只做收集与删除，关闭动作放到锁外
			c.clientPoolLock.Lock()
			var dead []*smuxClientInfo
			for id, info := range c.clientPool {
				if info.client.IsClosed() {
					delete(c.clientPool, id)
					dead = append(dead, info)
					log.Info("mux client", id, "is dead")
				} else if info.client.NumStreams() == 0 && time.Since(time.Unix(0, info.lastActiveTime.Load())) > idleTimeout {
					delete(c.clientPool, id)
					dead = append(dead, info)
					log.Info("mux client", id, "is closed due to inactivity")
				}
			}
			log.Debug("current mux clients: ", len(c.clientPool))
			for id, info := range c.clientPool {
				// Debugf 而不是 Debug(fmt.Sprintf(...))：默认级别下这 9 条拼接全是白付的
				// （f 变体先过 ShouldLog 再格式化）。这里的格式串与参数都不含引号或 HTML
				// 字符，两条清洗路径的输出逐字相同，且 caller 归因仍落在 cleanLoop 上
				log.Debugf("  - %x: %d/%d", id, info.client.NumStreams(), c.concurrency)
			}
			c.clientPoolLock.Unlock()
			for _, info := range dead {
				info.client.Close()       //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
				info.underlayConn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
			}
		case <-c.ctx.Done():
			log.Debug("shutting down mux cleaner..")
			c.clientPoolLock.Lock()
			pending := make([]*smuxClientInfo, 0, len(c.clientPool))
			for id, info := range c.clientPool {
				pending = append(pending, info)
				log.Debug("mux client", id, "closed")
			}
			clear(c.clientPool)
			c.clientPoolLock.Unlock()
			for _, info := range pending {
				info.client.Close()       //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
				info.underlayConn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
			}
			return
		}
	}
}

func (c *Client) newMuxClient() (*smuxClientInfo, error) {
	id := generateMuxID()

	tracker := log.NewConnectionTracker("MUX", "DialConn").
		WithField("mux_id", fmt.Sprintf("%x", id))

	c.clientPoolLock.Lock()
	if _, found := c.clientPool[id]; found {
		c.clientPoolLock.Unlock()
		return nil, common.NewError("duplicated id")
	}
	c.clientPoolLock.Unlock()

	fakeAddr := &tunnel.Address{
		DomainName:  "MUX_CONN",
		AddressType: tunnel.DomainName,
	}
	// Dial the underlying connection OUTSIDE the lock to avoid deadlock
	conn, err := c.underlay.DialConn(fakeAddr, &Tunnel{})
	if err != nil {
		_ = tracker.Error(err)
		return nil, common.NewError("mux failed to dial").Base(err)
	}
	conn = newStickyConn(conn)

	smuxConfig := smux.DefaultConfig()
	client, err := smux.Client(conn, smuxConfig)
	if err != nil {
		_ = tracker.Error(err)
		conn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
		return nil, common.NewError("mux failed to create client").Base(err)
	}
	_ = tracker.Success()

	info := &smuxClientInfo{
		client:       client,
		underlayConn: conn,
		id:           id,
	}
	info.lastActiveTime.Store(time.Now().UnixNano())
	c.clientPoolLock.Lock()
	// Close/cleanLoop 的 ctx.Done 分支排空池后就退出，之后没有任何东西会再回收
	// 池里的条目；此处若不检查 ctx，这条刚建好的会话就会落在排空之后，
	// 连同 stickyConn 的 fd 和 smux 的 readLoop/writeLoop 永久残留
	if err := c.ctx.Err(); err != nil {
		c.clientPoolLock.Unlock()
		client.Close() //gosec:disable -- 错误忽略：关停路径，尽力回收
		conn.Close()   //gosec:disable -- 错误忽略：关停路径，尽力回收
		return nil, common.NewError("mux client is shutting down").Base(err)
	}
	c.clientPool[id] = info
	c.clientPoolLock.Unlock()
	return info, nil
}

func (c *Client) DialConn(*tunnel.Address, tunnel.Tunnel) (tunnel.Conn, error) {
	if err := c.ctx.Err(); err != nil {
		return nil, common.NewError("mux client is shutting down").Base(err)
	}
	createNewConn := func(info *smuxClientInfo, streamTracker *log.ConnectionTracker) (tunnel.Conn, error) {
		rwc, err := info.client.OpenStream()
		info.lastActiveTime.Store(time.Now().UnixNano())
		if err != nil {
			_ = streamTracker.Error(err)
			info.underlayConn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
			info.client.Close()       //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
			c.clientPoolLock.Lock()
			delete(c.clientPool, info.id)
			c.clientPoolLock.Unlock()
			return nil, common.NewError("mux failed to open stream from client").Base(err)
		}
		_ = streamTracker.Success()
		return &Conn{
			rwc:            rwc,
			Conn:           info.underlayConn,
			lastActiveTime: &info.lastActiveTime,
			tracker:        streamTracker,
		}, nil
	}

	c.clientPoolLock.Lock()
	// First pass: try existing clients under lock
	var dead []*smuxClientInfo
	var usable *smuxClientInfo
	for id, info := range c.clientPool {
		if info.client.IsClosed() {
			// 只摘条目不够：它一旦离开池，cleanLoop 与 Client.Close 都再也看不见，
			// 底层那条 TCP 连接就没人关。回收判据必须与 cleanLoop 一致（双关），
			// 且要先把死条目收集起来、到锁外再关——旧写法在这里 delete 完就 continue，
			// 命中可用会话时更是直接 return，被摘掉的死条目从此悬在池外漏 fd。
			delete(c.clientPool, id)
			dead = append(dead, info)
			log.Infof("Mux client %x is closed", id)
			continue
		}
		if usable == nil && (info.client.NumStreams() < c.concurrency || c.concurrency <= 0) {
			usable = info
		}
	}
	c.clientPoolLock.Unlock()
	// 会话关闭（含 stickyConn 写 padding）可能阻塞，与 cleanLoop 同理放到锁外
	for _, info := range dead {
		info.client.Close()       //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
		info.underlayConn.Close() //gosec:disable -- 错误忽略：非关键路径或已通过其他方式处理
	}
	if usable != nil {
		streamTracker := log.NewConnectionTracker("MUX", "Stream").
			WithField("mux_id", fmt.Sprintf("%x", usable.id))
		return createNewConn(usable, streamTracker)
	}

	// No available client: dial a new one (outside the lock)
	info, err := c.newMuxClient()
	if err != nil {
		return nil, common.NewError("no available mux client found").Base(err)
	}
	streamTracker := log.NewConnectionTracker("MUX", "Stream").
		WithField("mux_id", fmt.Sprintf("%x", info.id))
	return createNewConn(info, streamTracker)
}

func (c *Client) DialPacket(tunnel.Tunnel) (tunnel.PacketConn, error) {
	return nil, common.NewError("mux does not support packet dial")
}

func NewClient(ctx context.Context, underlay tunnel.Client) (*Client, error) {
	clientConfig := config.FromContext(ctx, Name).(*Config)
	ctx, cancel := context.WithCancel(ctx)
	client := &Client{
		underlay:    underlay,
		concurrency: clientConfig.Mux.Concurrency,
		timeout:     time.Duration(clientConfig.Mux.IdleTimeout) * time.Second,
		ctx:         ctx,
		cancel:      cancel,
		clientPool:  make(map[muxID]*smuxClientInfo),
	}
	client.wg.Go(func() {
		client.cleanLoop()
	})
	log.Debug("mux client created")
	return client, nil
}
