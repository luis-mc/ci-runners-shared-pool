//go:build !linux && !darwin && !windows

package trayserver

import (
	"errors"
	"net"
)

// PeerOf cannot read the caller on this OS; the channel stays closed.
func PeerOf(net.Conn) (Caller, error) {
	return Caller{}, errors.New("caller identity is not supported on this OS")
}
