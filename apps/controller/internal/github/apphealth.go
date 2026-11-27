package github

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/secrets"
)

// AppHealthEvery is how often the App is checked after the check at start
// (github-integration R5.6).
const AppHealthEvery = time.Hour

// Words the console shows when the App is gone (R5.6). They are facts, not
// advice; the console adds the offer to run the wizard again.
const (
	ReasonAppDeleted = "GitHub does not know this App any more."
	ReasonKeyRefused = "GitHub refused the App's key."
	ReasonKeyMissing = "The App's key is not in this machine's secret store."
)

// AppState is what the last App check found.
type AppState struct {
	// AppID is the App the check was about.
	AppID int64
	// Gone is true when the App or its key is gone.
	Gone bool
	// Reason says why, when Gone.
	Reason string
	// CheckedAt is the time of the last answer that settled the question; zero
	// before the first.
	CheckedAt time.Time
}

// AppHealth reads GET /app with an App JWT at start and every hour (R5.6).
// While the App or its key is gone, GitHub work stops: callers ask Gone first.
// A job that is running is not touched; this type only answers a question.
type AppHealth struct {
	// Client is the one call needed.
	Client interface {
		GetApp(ctx context.Context) (App, error)
	}
	// AppID is the App in use now; 0 means none is set up. A verdict about an
	// older App is dropped when a new one is made by the wizard.
	AppID func() int64
	// Now is a seam for tests.
	Now func() time.Time

	mu sync.Mutex
	st AppState
}

// Check asks GitHub once. A transient failure (GitHub down, a rate limit, the
// secret store unavailable) keeps the last verdict and returns the error: a
// bad hour of GitHub must never stop work (R5.6).
func (h *AppHealth) Check(ctx context.Context) (AppState, error) {
	id := h.AppID()
	if id == 0 {
		return AppState{}, nil
	}
	_, err := h.Client.GetApp(Background(ctx))
	st := AppState{AppID: id, CheckedAt: h.now()}
	switch {
	case err == nil:
	case errors.Is(err, ErrNotFound):
		st.Gone, st.Reason = true, ReasonAppDeleted
	case errors.Is(err, ErrForbidden):
		st.Gone, st.Reason = true, ReasonKeyRefused
	case errors.Is(err, secrets.ErrNotFound):
		st.Gone, st.Reason = true, ReasonKeyMissing
	default:
		return h.State(), err
	}
	h.mu.Lock()
	h.st = st
	h.mu.Unlock()
	return st, nil
}

func (h *AppHealth) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

// State is the last verdict. It is the zero state when no check has settled
// for the App in use.
func (h *AppHealth) State() AppState {
	h.mu.Lock()
	st := h.st
	h.mu.Unlock()
	if st.AppID != h.AppID() {
		return AppState{}
	}
	return st
}

// Gone is true while the App or its key is gone.
func (h *AppHealth) Gone() bool { return h.State().Gone }

// Run checks at once and then every AppHealthEvery until ctx ends. Tick makes
// the hourly channel and its stop function; nil uses a real ticker. OnChange
// sees a change of the verdict (gone or back).
func (h *AppHealth) Run(ctx context.Context, tick func(time.Duration) (<-chan time.Time, func()), onChange func(AppState), onError func(error)) {
	if tick == nil {
		tick = func(d time.Duration) (<-chan time.Time, func()) {
			t := time.NewTicker(d)
			return t.C, t.Stop
		}
	}
	was := h.Gone()
	check := func() {
		st, err := h.Check(ctx)
		if err != nil {
			if onError != nil && ctx.Err() == nil {
				onError(err)
			}
			return
		}
		if st.Gone != was {
			was = st.Gone
			if onChange != nil {
				onChange(st)
			}
		}
	}
	check()
	c, stop := tick(AppHealthEvery)
	defer stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-c:
			check()
		}
	}
}
