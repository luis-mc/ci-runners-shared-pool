// Package trayserver is the service side of the tray's local channel (tray
// R5.8, R6.8): one JSON line in, one JSON line out, on a Unix socket or a
// named pipe that only this machine can reach. The service reads who is
// calling from the OS (peer credentials, pipe client token), never from the
// request.
package trayserver

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/tray"
)

// Caller is the OS user the OS says is on the other end.
type Caller struct {
	// OSUser is the Windows SID or the Unix uid, as stored in tray bindings.
	OSUser string
}

// Backend answers the calls. Errors are sent to the tray as the error text.
type Backend interface {
	// Status answers `status`.
	Status(ctx context.Context) (tray.Response, error)
	// Check answers `check`: look for a newer stable release now, then like Status.
	Check(ctx context.Context) (tray.Response, error)
	// Install answers `install`: the person said Install now. Drain, then install, then like Status.
	Install(ctx context.Context, c Caller) (tray.Response, error)
	// Cancel answers `cancel`: stop an update that downloads or waits for jobs, then like Status.
	Cancel(ctx context.Context, c Caller) (tray.Response, error)
	// Link answers `link`: a one-time URL for this caller, landing on target.
	Link(ctx context.Context, c Caller, target string) (string, error)
	// SetMode answers `mode`: set this machine's mode (automatic, always or
	// paused), then like Status.
	SetMode(ctx context.Context, c Caller, mode string) (tray.Response, error)
	// Pause answers `pause`: pause new jobs for one hour (one_hour) or until
	// Resume (until_resumed), then like Status.
	Pause(ctx context.Context, c Caller, kind string) (tray.Response, error)
	// Resume answers `resume`: end a pause and the Paused mode, then like Status.
	Resume(ctx context.Context, c Caller) (tray.Response, error)
}

// ErrNoPeer says the OS could not name the caller; the channel then answers nothing but an error (spec section 8: closed, no link).
var ErrNoPeer = errors.New("the caller's OS user cannot be read")

// PeerFunc reads the caller of a connection from the OS.
type PeerFunc func(net.Conn) (Caller, error)

// Server serves the channel.
type Server struct {
	Backend Backend
	Peer    PeerFunc // nil: the OS reader of this build
	Logger  *slog.Logger
	// Timeout bounds one request (default 10 s; `check` asks GitHub, so it is not short).
	Timeout time.Duration
}

const maxLine = 4 << 10

// Serve accepts connections until ctx ends or the listener fails.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	if s.Peer == nil {
		s.Peer = PeerOf
	}
	if s.Logger == nil {
		s.Logger = slog.Default()
	}
	if s.Timeout <= 0 {
		s.Timeout = 10 * time.Second
	}
	go func() { <-ctx.Done(); _ = ln.Close() }()
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			return err
		}
		wg.Add(1)
		go func() { defer wg.Done(); s.handle(ctx, conn) }()
	}
}

func (s *Server) handle(ctx context.Context, conn net.Conn) {
	defer func() { _ = conn.Close() }()
	ctx, cancel := context.WithTimeout(ctx, s.Timeout)
	defer cancel()
	_ = conn.SetDeadline(time.Now().Add(s.Timeout))
	reply := func(r tray.Response) {
		b, _ := json.Marshal(r)
		_, _ = conn.Write(append(b, '\n'))
	}
	fail := func(msg string) { reply(tray.Response{OK: false, Error: msg}) }

	who, err := s.Peer(conn)
	if err != nil {
		s.Logger.Warn("tray channel: caller unknown, closed", "err", err)
		fail(ErrNoPeer.Error())
		return
	}
	line, err := bufio.NewReaderSize(io.LimitReader(conn, maxLine), maxLine).ReadBytes('\n')
	if err != nil && (!errors.Is(err, io.EOF) || len(line) == 0) {
		return
	}
	var req tray.Request
	if err := json.Unmarshal(line, &req); err != nil {
		fail("bad request")
		return
	}
	switch req.Call {
	case "status":
		r, err := s.Backend.Status(ctx)
		answer(reply, fail, r, err)
	case "check":
		r, err := s.Backend.Check(ctx)
		answer(reply, fail, r, err)
	case "install":
		r, err := s.Backend.Install(ctx, who)
		answer(reply, fail, r, err)
	case "cancel":
		r, err := s.Backend.Cancel(ctx, who)
		answer(reply, fail, r, err)
	case "mode":
		r, err := s.Backend.SetMode(ctx, who, req.Mode)
		answer(reply, fail, r, err)
	case "pause":
		r, err := s.Backend.Pause(ctx, who, req.Pause)
		answer(reply, fail, r, err)
	case "resume":
		r, err := s.Backend.Resume(ctx, who)
		answer(reply, fail, r, err)
	case "link":
		u, err := s.Backend.Link(ctx, who, req.Target)
		if err != nil {
			fail(err.Error())
			return
		}
		reply(tray.Response{OK: true, URL: u})
	default:
		fail("unknown call")
	}
}

func answer(reply func(tray.Response), fail func(string), r tray.Response, err error) {
	if err != nil {
		fail(err.Error())
		return
	}
	r.OK = true
	reply(r)
}
