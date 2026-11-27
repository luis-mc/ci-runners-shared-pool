package metrics

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
)

// Store keeps the Config. The controller wires one on its settings, and writes
// the audit row of a change there.
type Store interface {
	Load(ctx context.Context) (Config, error)
	Save(ctx context.Context, actor audit.Actor, c Config) error
}

// TokenStore keeps the bearer token in the secret store. Get returns "" when no
// token was made.
type TokenStore interface {
	Get(ctx context.Context) (string, error)
	Put(ctx context.Context, token string) error
}

// InvalidError lists what is wrong with a Config.
type InvalidError struct{ Problems []string }

func (e *InvalidError) Error() string { return strings.Join(e.Problems, " ") }

// ErrNoToken is returned when the page needs a token and none was made.
var ErrNoToken error = problem("Make a token first, or turn the token off.")

// State is what the settings screen shows.
type State struct {
	Config       Config
	TokenSet     bool
	Running      bool   // the /metrics page is up
	Addr         string // where it listens now
	Pushing      bool
	LastPush     time.Time
	LastPushErr  string
	ScrapeConfig string
}

// Service owns the settings, the listener and the push loop.
type Service struct {
	Store   Store
	Token   TokenStore
	Source  Source
	Version string
	// Host is the name Prometheus reaches this PC at, for the scrape text.
	Host string
	// Start opens the listener; the default is Start. A test replaces it.
	Start func(addr string, h http.Handler) (*Server, error)
	// OnError reports a failed push to the log.
	OnError func(error)

	mu       sync.Mutex
	srv      *Server
	cancel   context.CancelFunc
	lastPush time.Time
	lastErr  string
	pushing  bool
}

// Apply starts, restarts or stops the page and the push to match the stored
// settings. The controller calls it once at start-up.
func (s *Service) Apply(ctx context.Context) error {
	c, err := s.Store.Load(ctx)
	if err != nil {
		return err
	}
	return s.apply(ctx, c)
}

func (s *Service) apply(ctx context.Context, c Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopLocked()
	var errs []error
	if c.Enabled {
		token := ""
		if c.RequireToken {
			t, err := s.Token.Get(ctx)
			if err != nil {
				return err
			}
			if t == "" {
				return ErrNoToken // fail closed: never serve open when a token is asked for
			}
			token = t
		}
		start := s.Start
		if start == nil {
			start = Start
		}
		srv, err := start(c.ListenAddr(), Handler(s.Source, token))
		if err != nil {
			errs = append(errs, err)
		} else {
			s.srv = srv
		}
	}
	if c.PushEnabled {
		u, err := OTLPURL(c.PushEndpoint)
		if err != nil {
			return err
		}
		pctx, cancel := context.WithCancel(context.Background())
		s.cancel = cancel
		s.pushing = true
		p := &Pusher{Source: s.Source, Endpoint: u, Interval: time.Duration(c.PushSeconds) * time.Second, Version: s.Version}
		p.OnError = func(err error) {
			s.mu.Lock()
			s.lastErr = err.Error()
			s.mu.Unlock()
			if s.OnError != nil {
				s.OnError(err)
			}
		}
		p.OnPush = func(at time.Time) {
			s.mu.Lock()
			s.lastPush, s.lastErr = at, ""
			s.mu.Unlock()
		}
		go p.Run(pctx)
	}
	return errors.Join(errs...)
}

func (s *Service) stopLocked() {
	if s.srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = s.srv.Close(ctx)
		cancel()
		s.srv = nil
	}
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	s.pushing = false
}

// Close stops everything.
func (s *Service) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopLocked()
}

// State reads the settings and the live parts.
func (s *Service) State(ctx context.Context) (State, error) {
	c, err := s.Store.Load(ctx)
	if err != nil {
		return State{}, err
	}
	t, err := s.Token.Get(ctx)
	if err != nil {
		return State{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st := State{Config: c, TokenSet: t != "", Pushing: s.pushing, LastPush: s.lastPush, LastPushErr: s.lastErr, ScrapeConfig: ScrapeConfig(c, s.Host)}
	if s.srv != nil {
		st.Running, st.Addr = true, s.srv.Addr()
	}
	return st, nil
}

// Update checks c, keeps it and applies it. A bad Config changes nothing.
func (s *Service) Update(ctx context.Context, actor audit.Actor, c Config) (State, error) {
	if p := c.Validate(); len(p) > 0 {
		return State{}, &InvalidError{Problems: p}
	}
	if c.Enabled && c.RequireToken {
		if t, err := s.Token.Get(ctx); err != nil {
			return State{}, err
		} else if t == "" {
			return State{}, ErrNoToken
		}
	}
	if err := s.Store.Save(ctx, actor, c); err != nil {
		return State{}, err
	}
	applyErr := s.apply(ctx, c)
	st, err := s.State(ctx)
	if err != nil {
		return State{}, err
	}
	return st, applyErr
}

// MakeToken makes and keeps a new token and returns it. It is the only time the
// token is shown. A running page restarts with it.
func (s *Service) MakeToken(ctx context.Context) (string, error) {
	t, err := NewToken()
	if err != nil {
		return "", err
	}
	if err := s.Token.Put(ctx, t); err != nil {
		return "", err
	}
	if c, err := s.Store.Load(ctx); err == nil && c.Enabled {
		_ = s.apply(ctx, c)
	}
	return t, nil
}
