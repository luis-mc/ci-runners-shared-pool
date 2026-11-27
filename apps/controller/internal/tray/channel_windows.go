//go:build windows

package tray

import (
	"context"
	"io"
	"net"
	"time"

	"golang.org/x/sys/windows"
)

// PipeName is the service's named pipe (spec section 7).
const PipeName = `\\.\pipe\crsp-tray`

// DefaultDialer opens the named pipe as a client.
func DefaultDialer() Dialer {
	return func(ctx context.Context) (net.Conn, error) {
		name, err := windows.UTF16PtrFromString(PipeName)
		if err != nil {
			return nil, err
		}
		h, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil,
			windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL|windows.SECURITY_SQOS_PRESENT|windows.SECURITY_IDENTIFICATION, 0)
		if err != nil {
			return nil, err
		}
		return &pipeConn{h: h}, nil
	}
}

type pipeConn struct{ h windows.Handle }

func (p *pipeConn) Read(b []byte) (int, error) {
	var n uint32
	err := windows.ReadFile(p.h, b, &n, nil)
	if err == windows.ERROR_BROKEN_PIPE {
		return int(n), io.EOF
	}
	return int(n), err
}
func (p *pipeConn) Write(b []byte) (int, error) {
	var n uint32
	err := windows.WriteFile(p.h, b, &n, nil)
	return int(n), err
}
func (p *pipeConn) Close() error                     { return windows.CloseHandle(p.h) }
func (p *pipeConn) LocalAddr() net.Addr              { return pipeAddr{} }
func (p *pipeConn) RemoteAddr() net.Addr             { return pipeAddr{} }
func (p *pipeConn) SetDeadline(time.Time) error      { return nil }
func (p *pipeConn) SetReadDeadline(time.Time) error  { return nil }
func (p *pipeConn) SetWriteDeadline(time.Time) error { return nil }

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return PipeName }
