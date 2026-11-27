//go:build windows

package trayserver

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/tray"
)

// The pipe's access: the service (SYSTEM) and administrators in full, every
// signed-in local user may read and write. Remote clients are refused by
// PIPE_REJECT_REMOTE_CLIENTS.
const pipeSDDL = "D:(A;;GA;;;SY)(A;;GA;;;BA)(A;;GRGW;;;AU)"

// DefaultPath is unused on Windows: the pipe name is fixed.
func DefaultPath() string { return "" }

// Listen opens the named pipe `\\.\pipe\crsp-tray`.
func Listen(_ string, _ *slog.Logger) (net.Listener, error) {
	sd, err := windows.SecurityDescriptorFromString(pipeSDDL)
	if err != nil {
		return nil, err
	}
	l := &pipeListener{sd: sd, done: make(chan struct{})}
	// Make the first instance now so a bad name or a taken pipe fails here.
	h, err := l.create(true)
	if err != nil {
		return nil, err
	}
	l.first = h
	return l, nil
}

type pipeListener struct {
	sd    *windows.SECURITY_DESCRIPTOR
	mu    sync.Mutex
	first windows.Handle
	done  chan struct{}
	once  sync.Once
}

func (l *pipeListener) create(first bool) (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString(tray.PipeName)
	if err != nil {
		return 0, err
	}
	sa := &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: l.sd}
	flags := uint32(windows.PIPE_ACCESS_DUPLEX)
	if first {
		flags |= windows.FILE_FLAG_FIRST_PIPE_INSTANCE
	}
	return windows.CreateNamedPipe(name, flags,
		windows.PIPE_TYPE_BYTE|windows.PIPE_READMODE_BYTE|windows.PIPE_WAIT|windows.PIPE_REJECT_REMOTE_CLIENTS,
		windows.PIPE_UNLIMITED_INSTANCES, 4096, 4096, 0, sa)
}

func (l *pipeListener) Accept() (net.Conn, error) {
	l.mu.Lock()
	h := l.first
	l.first = 0
	l.mu.Unlock()
	if h == 0 {
		var err error
		if h, err = l.create(false); err != nil {
			return nil, err
		}
	}
	err := windows.ConnectNamedPipe(h, nil)
	if err != nil && !errors.Is(err, windows.ERROR_PIPE_CONNECTED) {
		_ = windows.CloseHandle(h)
		select {
		case <-l.done:
			return nil, net.ErrClosed
		default:
		}
		return nil, err
	}
	return &serverPipe{h: h}, nil
}

func (l *pipeListener) Close() error {
	l.once.Do(func() { close(l.done) })
	// Wake a blocked Accept with a throw-away client.
	if c, err := tray.DefaultDialer()(context.Background()); err == nil {
		_ = c.Close()
	}
	return nil
}

func (l *pipeListener) Addr() net.Addr { return pipeAddr{} }

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return tray.PipeName }

type serverPipe struct{ h windows.Handle }

func (p *serverPipe) Read(b []byte) (int, error) {
	var n uint32
	err := windows.ReadFile(p.h, b, &n, nil)
	if err == windows.ERROR_BROKEN_PIPE {
		return int(n), io.EOF
	}
	return int(n), err
}
func (p *serverPipe) Write(b []byte) (int, error) {
	var n uint32
	err := windows.WriteFile(p.h, b, &n, nil)
	return int(n), err
}
func (p *serverPipe) Close() error {
	_ = windows.FlushFileBuffers(p.h)
	_ = windows.DisconnectNamedPipe(p.h)
	return windows.CloseHandle(p.h)
}
func (p *serverPipe) LocalAddr() net.Addr              { return pipeAddr{} }
func (p *serverPipe) RemoteAddr() net.Addr             { return pipeAddr{} }
func (p *serverPipe) SetDeadline(time.Time) error      { return nil }
func (p *serverPipe) SetReadDeadline(time.Time) error  { return nil }
func (p *serverPipe) SetWriteDeadline(time.Time) error { return nil }

// PeerOf reads the caller's SID: the pipe names the client process, and the
// process token names its user.
func PeerOf(c net.Conn) (Caller, error) {
	p, ok := c.(*serverPipe)
	if !ok {
		return Caller{}, errors.New("not a pipe")
	}
	var pid uint32
	if err := windows.GetNamedPipeClientProcessId(p.h, &pid); err != nil {
		return Caller{}, err
	}
	proc, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return Caller{}, err
	}
	defer func() { _ = windows.CloseHandle(proc) }()
	var tok windows.Token
	if err := windows.OpenProcessToken(proc, windows.TOKEN_QUERY, &tok); err != nil {
		return Caller{}, err
	}
	defer func() { _ = tok.Close() }()
	u, err := tok.GetTokenUser()
	if err != nil {
		return Caller{}, err
	}
	return Caller{OSUser: u.User.Sid.String()}, nil
}
