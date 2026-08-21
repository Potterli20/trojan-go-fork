package freedom

import (
	"net"
	"sync"
)

type outboundState struct {
	mu      sync.RWMutex
	localIP net.IP
	fwmark  int
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

func SetGlobalOutbound(localIP net.IP, fwmark int) (net.IP, int) {
	globalOutbound.mu.Lock()
	defer globalOutbound.mu.Unlock()
	prevIP, prevMark := globalOutbound.localIP, globalOutbound.fwmark
	globalOutbound.localIP = localIP
	globalOutbound.fwmark = fwmark
	return prevIP, prevMark
}

func resetGlobalOutbound() {
	globalOutbound.mu.Lock()
	defer globalOutbound.mu.Unlock()
	globalOutbound.localIP = nil
	globalOutbound.fwmark = 0
}
