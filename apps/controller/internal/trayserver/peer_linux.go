//go:build linux

package trayserver

import (
	"errors"
	"net"
	"strconv"

	"golang.org/x/sys/unix"
)

// PeerOf reads the caller's uid from the socket (SO_PEERCRED).
func PeerOf(c net.Conn) (Caller, error) {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return Caller{}, errors.New("not a unix socket")
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return Caller{}, err
	}
	var cred *unix.Ucred
	var serr error
	if err := raw.Control(func(fd uintptr) { cred, serr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED) }); err != nil {
		return Caller{}, err
	}
	if serr != nil {
		return Caller{}, serr
	}
	return Caller{OSUser: strconv.FormatUint(uint64(cred.Uid), 10)}, nil
}
