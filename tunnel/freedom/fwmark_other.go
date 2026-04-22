//go:build !linux

package freedom

import "syscall"

func fwmarkControl(fwmark int) func(network, address string, c syscall.RawConn) error {
	return nil
}

const fwmarkSupported = false
