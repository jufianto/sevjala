//go:build !windows && !linux && !darwin

package localapi

import "net"

func peerUID(net.Conn) (uint32, bool) { return 0, false }
