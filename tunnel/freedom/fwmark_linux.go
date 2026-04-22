//go:build linux

package freedom

import (
	"syscall"

	"golang.org/x/sys/unix"
)

func fwmarkControl(fwmark int) func(network, address string, c syscall.RawConn) error {
	if fwmark == 0 {
		return nil
	}
	return func(network, address string, c syscall.RawConn) error {
		var setErr error
		err := c.Control(func(fd uintptr) {
			setErr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_MARK, fwmark)
		})
		if err != nil {
			return err
		}
		return setErr
	}
}

const fwmarkSupported = true
