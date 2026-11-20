package freedom

import (
	"net"
	"sync"
)

type outboundState struct {
	mu      sync.RWMutex
	localIP net.IP
	fwmark  int
	// override 表示 API 已在运行期显式设置过全局出网（SetOutboundConfig RPC）。
	// 置位后它对所有 freedom 实例生效（含之后新建的）；未置位时每个实例拨号
	// 用自己构造时的配置，互不覆盖（R18-1）。
	override bool
}

var globalOutbound outboundState

func GetGlobalOutbound() (net.IP, int) {
	return getGlobalOutbound()
}

func getGlobalOutbound() (net.IP, int) {
	globalOutbound.mu.RLock()
	defer globalOutbound.mu.RUnlock()
	return globalOutbound.localIP, globalOutbound.fwmark
}

// outboundOverridden 报告运行期覆盖是否已置位。
func outboundOverridden() bool {
	globalOutbound.mu.RLock()
	defer globalOutbound.mu.RUnlock()
	return globalOutbound.override
}

// SetGlobalOutbound 是 API 的运行期覆盖入口：置位 override，之后所有实例（含
// 已存在与后续新建）的拨号都用这份值，直到 resetGlobalOutbound。
func SetGlobalOutbound(localIP net.IP, fwmark int) (net.IP, int) {
	globalOutbound.mu.Lock()
	defer globalOutbound.mu.Unlock()
	prevIP, prevMark := globalOutbound.localIP, globalOutbound.fwmark
	globalOutbound.localIP = localIP
	globalOutbound.fwmark = fwmark
	globalOutbound.override = true
	return prevIP, prevMark
}

// applyConfiguredOutbound 是 NewClient 专用的装配入口：把配置解析出的值登记为
// 实例默认。覆盖未置位时同步写进全局（GetOutboundConfig 报告的就是它）；覆盖
// 已置位时新实例继承覆盖值而不是用自己的配置——API 的运行期决定优先。
func applyConfiguredOutbound(localIP net.IP, fwmark int) (net.IP, int) {
	globalOutbound.mu.Lock()
	defer globalOutbound.mu.Unlock()
	if globalOutbound.override {
		return globalOutbound.localIP, globalOutbound.fwmark
	}
	globalOutbound.localIP = localIP
	globalOutbound.fwmark = fwmark
	return localIP, fwmark
}

func resetGlobalOutbound() {
	globalOutbound.mu.Lock()
	defer globalOutbound.mu.Unlock()
	globalOutbound.localIP = nil
	globalOutbound.fwmark = 0
	globalOutbound.override = false
}
