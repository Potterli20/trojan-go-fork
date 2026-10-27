package quic

import (
	"fmt"
	"github.com/Potterli20/trojan-go-fork/common"
	"time"

	"github.com/Potterli20/trojan-go-fork/log"
	"github.com/apernet/quic-go"
	xrayCongestion "github.com/xtls/xray-core/transport/internet/hysteria/congestion"
)

// CongestionConfig 封装拥塞控制配置
type CongestionConfig struct {
	Algorithm  string
	BrutalUp   uint64
	BrutalDown uint64
}

// CongestionStatus 记录拥塞控制状态信息
type CongestionStatus struct {
	Algorithm      string
	Role           string
	BrutalUp       uint64
	BrutalDown     uint64
	EffectiveSpeed uint64
	Success        bool
	ErrorMessage   string
}

// ApplyCongestionControl 应用拥塞控制算法到QUIC连接
// 入口参数：
//   - conn: QUIC连接对象
//   - config: 拥塞控制配置（算法类型、上下行速率限制）
//   - role: 角色标识（server/client）
//
// 出口结果：
//   - 返回CongestionStatus结构，包含算法应用状态和相关参数
//
// ValidateBrutalParams 在建栈期就拦住"选了 brutal 却没给码率"。
// 过去这种配置只在运行期打一行 Warn，连接照常用库默认 BBR 上线——用户以为链路被
// 固定码率整形（brutal 存在的唯一理由），实际没有任何东西生效，运行期也没有观测点。
// 口径同 api.CheckExposure / 空密码那两条：策略性拒绝必须由 NewClient/NewServer 同步
// 返回，让建栈失败、进程以非零退出码收场。
func ValidateBrutalParams(c CongestionConfig) error {
	if c.Algorithm != "brutal" && c.Algorithm != "force-brutal" {
		return nil
	}
	if c.BrutalUp == 0 || c.BrutalDown == 0 {
		return common.NewErrorf("congestion %q requires positive brutal-up and brutal-down (got up=%d down=%d)",
			c.Algorithm, c.BrutalUp, c.BrutalDown)
	}
	return nil
}

func ApplyCongestionControl(conn *quic.Conn, config CongestionConfig, role string) CongestionStatus {
	startTime := time.Now()

	// 本层的日志一律"先过级别门禁，再动参数"：log.Debug/Debugf 的门禁在函数内部，
	// 而 fmt.Sprintf / time.Now().Format 在调用点就已经付掉了（口径见 AGENTS.md 的
	// 日志约定与 tunnel/freedom/conn.go）。ENTRY/EXIT 这两条是每条 QUIC 连接一次。
	if log.ShouldLog(log.DebugLevel) {
		log.Debugf("[%s] [ApplyCongestionControl] [ENTRY] Timestamp=%d, Role=%s, Algorithm=%q, BrutalUp=%d bps, BrutalDown=%d bps, Conn=%p",
			startTime.Format("2006-01-02 15:04:05.000"),
			startTime.UnixNano(),
			role,
			config.Algorithm,
			config.BrutalUp,
			config.BrutalDown,
			conn,
		)
	}

	status := CongestionStatus{
		Algorithm:  config.Algorithm,
		Role:       role,
		BrutalUp:   config.BrutalUp,
		BrutalDown: config.BrutalDown,
		Success:    true,
	}

	algorithm := config.Algorithm
	if algorithm == "" {
		if log.ShouldLog(log.DebugLevel) {
			log.Debugf("[%s] [ApplyCongestionControl] Algorithm not specified, defaulting to BBR",
				time.Now().Format("2006-01-02 15:04:05.000"))
		}
		algorithm = "bbr"
		status.Algorithm = "bbr"
	}

	if conn == nil {
		status.Success = false
		status.ErrorMessage = "conn is nil"
		log.Warnf("[%s] [ApplyCongestionControl] Failed: %s",
			time.Now().Format("2006-01-02 15:04:05.000"),
			status.ErrorMessage)
		return status
	}

	switch algorithm {
	case "brutal":
		if config.BrutalUp > 0 && config.BrutalDown > 0 {
			speed := min(config.BrutalUp, config.BrutalDown)
			// xray-core 新版 UseBrutal 增加了 disableLossCompensation 参数，
			// 传 false 保持旧版"丢包时维持固定速率"的 brutal 语义
			xrayCongestion.UseBrutal(conn, speed, false)
			status.EffectiveSpeed = speed
			if log.ShouldLog(log.DebugLevel) {
				log.Debugf("[%s] [ApplyCongestionControl] [BRUTAL] Applied brutal congestion control with speed=%d bps, Conn=%p",
					time.Now().Format("2006-01-02 15:04:05.000"),
					speed,
					conn)
			}
		} else {
			status.Success = false
			status.ErrorMessage = "Brutal congestion control requires both brutal_up and brutal_down to be set"
			log.Warnf("[%s] [ApplyCongestionControl] [BRUTAL] Failed: %s",
				time.Now().Format("2006-01-02 15:04:05.000"),
				status.ErrorMessage)
		}
	case "force-brutal":
		if config.BrutalUp > 0 {
			xrayCongestion.UseBrutal(conn, config.BrutalUp, false)
			status.EffectiveSpeed = config.BrutalUp
			if log.ShouldLog(log.DebugLevel) {
				log.Debugf("[%s] [ApplyCongestionControl] [FORCE-BRUTAL] Applied force-brutal congestion control with speed=%d bps, Conn=%p",
					time.Now().Format("2006-01-02 15:04:05.000"),
					config.BrutalUp,
					conn)
			}
		} else {
			status.Success = false
			status.ErrorMessage = "Force-brutal congestion control requires brutal_up to be set"
			log.Warnf("[%s] [ApplyCongestionControl] [FORCE-BRUTAL] Failed: %s",
				time.Now().Format("2006-01-02 15:04:05.000"),
				status.ErrorMessage)
		}
	case "bbr":
		xrayCongestion.UseBBR(conn, "standard")
		if log.ShouldLog(log.DebugLevel) {
			log.Debugf("[%s] [ApplyCongestionControl] [BBR] Applied BBR congestion control, Conn=%p",
				time.Now().Format("2006-01-02 15:04:05.000"),
				conn)
		}
	case "reno":
		if log.ShouldLog(log.DebugLevel) {
			log.Debugf("[%s] [ApplyCongestionControl] [RENO] Applied Reno congestion control, Conn=%p",
				time.Now().Format("2006-01-02 15:04:05.000"),
				conn)
		}
	default:
		status.Success = false
		status.ErrorMessage = fmt.Sprintf("Unknown congestion control: %s, falling back to BBR", algorithm)
		log.Warnf("[%s] [ApplyCongestionControl] [UNKNOWN] %s",
			time.Now().Format("2006-01-02 15:04:05.000"),
			status.ErrorMessage)
		xrayCongestion.UseBBR(conn, "standard")
		status.Algorithm = "bbr"
		if log.ShouldLog(log.DebugLevel) {
			log.Debugf("[%s] [ApplyCongestionControl] [BBR] Applied BBR congestion control as fallback, Conn=%p",
				time.Now().Format("2006-01-02 15:04:05.000"),
				conn)
		}
	}

	duration := time.Since(startTime)
	if log.ShouldLog(log.DebugLevel) {
		log.Debugf("[%s] [ApplyCongestionControl] [EXIT] Timestamp=%d, Role=%s, Algorithm=%q, Success=%t, EffectiveSpeed=%d bps, Duration=%v, Error=%q",
			time.Now().Format("2006-01-02 15:04:05.000"),
			time.Now().UnixNano(),
			role,
			status.Algorithm,
			status.Success,
			status.EffectiveSpeed,
			duration,
			status.ErrorMessage,
		)
	}

	return status
}
