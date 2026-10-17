package localapi

import (
	"net"

	"golang.org/x/sys/unix"
)

func peerUID(c net.Conn) (uint32, bool) {
	var uid uint32
	ok := false
	control(c, func(fd int) {
		if cred, err := unix.GetsockoptUcred(fd, unix.SOL_SOCKET, unix.SO_PEERCRED); err == nil {
			uid, ok = cred.Uid, true
		}
	})
	return uid, ok
}
