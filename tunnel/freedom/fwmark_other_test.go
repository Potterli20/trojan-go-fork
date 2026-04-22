//go:build !linux

package freedom

import (
	"testing"

	"github.com/p4gefau1t/trojan-go/common"
)

func TestFwmark_NonLinux_IgnoredWithWarning(t *testing.T) {
	ctx := newCtxWithConfig(&Config{OutboundFwmark: 0x42})
	c, err := NewClient(ctx, nil)
	common.Must(err)
	defer c.Close()
	if c.outboundFwmark != 0 {
		t.Fatalf("non-linux platform must reset fwmark to 0, got %#x", c.outboundFwmark)
	}
}
