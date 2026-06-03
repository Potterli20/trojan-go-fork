package quic

import (
	"fmt"
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
func ApplyCongestionControl(conn *quic.Conn, config CongestionConfig, role string) CongestionStatus {
	startTime := time.Now()

	log.Debug(fmt.Sprintf("[%s] [ApplyCongestionControl] [ENTRY] Timestamp=%d, Role=%s, Algorithm=%q, BrutalUp=%d bps, BrutalDown=%d bps, Conn=%p",
		startTime.Format("2006-01-02 15:04:05.000"),
		startTime.UnixNano(),
		role,
		config.Algorithm,
		config.BrutalUp,
		config.BrutalDown,
		conn,
	))

	status := CongestionStatus{
		Algorithm:  config.Algorithm,
		Role:       role,
		BrutalUp:   config.BrutalUp,
		BrutalDown: config.BrutalDown,
		Success:    true,
	}

	algorithm := config.Algorithm
	if algorithm == "" {
		log.Debug(fmt.Sprintf("[%s] [ApplyCongestionControl] Algorithm not specified, defaulting to BBR",
			time.Now().Format("2006-01-02 15:04:05.000")))
		algorithm = "bbr"
		status.Algorithm = "bbr"
	}

	if conn == nil {
		status.Success = false
		status.ErrorMessage = "conn is nil"
		log.Warn(fmt.Sprintf("[%s] [ApplyCongestionControl] Failed: %s",
			time.Now().Format("2006-01-02 15:04:05.000"),
			status.ErrorMessage))
		return status
	}

	switch algorithm {
	case "brutal":
		if config.BrutalUp > 0 && config.BrutalDown > 0 {
			speed := minUint64(config.BrutalUp, config.BrutalDown)
			xrayCongestion.UseBrutal(conn, speed)
			status.EffectiveSpeed = speed
			log.Debug(fmt.Sprintf("[%s] [ApplyCongestionControl] [BRUTAL] Applied brutal congestion control with speed=%d bps, Conn=%p",
				time.Now().Format("2006-01-02 15:04:05.000"),
				speed,
				conn))
		} else {
			status.Success = false
			status.ErrorMessage = "Brutal congestion control requires both brutal_up and brutal_down to be set"
			log.Warn(fmt.Sprintf("[%s] [ApplyCongestionControl] [BRUTAL] Failed: %s",
				time.Now().Format("2006-01-02 15:04:05.000"),
				status.ErrorMessage))
		}
	case "force-brutal":
		if config.BrutalUp > 0 {
			xrayCongestion.UseBrutal(conn, config.BrutalUp)
			status.EffectiveSpeed = config.BrutalUp
			log.Debug(fmt.Sprintf("[%s] [ApplyCongestionControl] [FORCE-BRUTAL] Applied force-brutal congestion control with speed=%d bps, Conn=%p",
				time.Now().Format("2006-01-02 15:04:05.000"),
				config.BrutalUp,
				conn))
		} else {
			status.Success = false
			status.ErrorMessage = "Force-brutal congestion control requires brutal_up to be set"
			log.Warn(fmt.Sprintf("[%s] [ApplyCongestionControl] [FORCE-BRUTAL] Failed: %s",
				time.Now().Format("2006-01-02 15:04:05.000"),
				status.ErrorMessage))
		}
	case "bbr":
		xrayCongestion.UseBBR(conn, "standard")
		log.Debug(fmt.Sprintf("[%s] [ApplyCongestionControl] [BBR] Applied BBR congestion control, Conn=%p",
			time.Now().Format("2006-01-02 15:04:05.000"),
			conn))
	case "reno":
		log.Debug(fmt.Sprintf("[%s] [ApplyCongestionControl] [RENO] Applied Reno congestion control, Conn=%p",
			time.Now().Format("2006-01-02 15:04:05.000"),
			conn))
	default:
		status.Success = false
		status.ErrorMessage = fmt.Sprintf("Unknown congestion control: %s, falling back to BBR", algorithm)
		log.Warn(fmt.Sprintf("[%s] [ApplyCongestionControl] [UNKNOWN] %s",
			time.Now().Format("2006-01-02 15:04:05.000"),
			status.ErrorMessage))
		xrayCongestion.UseBBR(conn, "standard")
		status.Algorithm = "bbr"
		log.Debug(fmt.Sprintf("[%s] [ApplyCongestionControl] [BBR] Applied BBR congestion control as fallback, Conn=%p",
			time.Now().Format("2006-01-02 15:04:05.000"),
			conn))
	}

	duration := time.Since(startTime)
	log.Debug(fmt.Sprintf("[%s] [ApplyCongestionControl] [EXIT] Timestamp=%d, Role=%s, Algorithm=%q, Success=%t, EffectiveSpeed=%d bps, Duration=%v, Error=%q",
		time.Now().Format("2006-01-02 15:04:05.000"),
		time.Now().UnixNano(),
		role,
		status.Algorithm,
		status.Success,
		status.EffectiveSpeed,
		duration,
		status.ErrorMessage,
	))

	return status
}

func minUint64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}
