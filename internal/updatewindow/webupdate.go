package updatewindow

import (
	"context"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/tray"
)

// UpdateScreen is the update window as a web page (macOS and Linux, TRAY7). It is the Windows
// window's Model with a different painter: the same states, the same words, the same buttons.
type UpdateScreen struct {
	Svc  Service
	Open func(url string) error // opens the release notes; nil: the OS default handler

	mu    sync.Mutex
	model *Model
	busy  map[ButtonID]string
}

// NewUpdateScreen starts the screen for a configuration. It reads the service once first,
// so the first picture is the right one (a stage left by an earlier update is not shown).
func NewUpdateScreen(c Config, svc Service) *UpdateScreen {
	s := &UpdateScreen{Svc: svc, model: NewModel(c.From, c.To, c.Mode), Open: tray.OSLauncher}
	if svc != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		st, err := svc.Status(ctx)
		cancel()
		s.model.Observe(SnapshotOf(st, err))
		if s.model.From == "" && err == nil {
			s.model.From = st.Version
		}
	}
	return s
}

// Name is the one-window name of the update.
func (*UpdateScreen) Name() string { return "update-window" }

// Title is the title of the window.
func (*UpdateScreen) Title() string { return WindowTitle }

// Size is the size of the window.
func (*UpdateScreen) Size() (int, int) { return UpdateWidth, UpdateHeight }

// Page is the picture of the state now.
func (s *UpdateScreen) Page() PageView {
	s.mu.Lock()
	defer s.mu.Unlock()
	return toPage(s.model.View(), s.busy)
}

// Tick reads the service. No answer is the restart, not an error (the model knows).
func (s *UpdateScreen) Tick(ctx context.Context) {
	if s.Svc == nil {
		return
	}
	st, err := s.Svc.Status(ctx)
	s.mu.Lock()
	s.model.Observe(SnapshotOf(st, err))
	s.mu.Unlock()
}

// Press does what a button says (the same as the Windows window).
func (s *UpdateScreen) Press(id string, _ map[string]bool) bool {
	b := ButtonID(id)
	s.mu.Lock()
	if _, busy := s.busy[b]; busy {
		s.mu.Unlock()
		return false
	}
	valid := false
	for _, x := range s.model.View().Buttons {
		if x.ID == b && !x.Disabled {
			valid = true
		}
	}
	version := s.model.doneVersion()
	s.mu.Unlock()
	if !valid {
		return false // a button the step does not show cannot be pressed (UX-30)
	}
	switch b {
	case BtnWhatsNew:
		if open := s.Open; open != nil {
			_ = open(releaseURL(version))
		}
	case BtnNotNow, BtnHide, BtnClose:
		return true // the update, if any, goes on in the service
	case BtnUpdate:
		if s.Svc == nil {
			return false
		}
		s.mu.Lock()
		s.model.Requested()
		s.mu.Unlock()
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), webInstallMax)
			defer cancel()
			_, err := s.Svc.Install(ctx)
			s.mu.Lock()
			s.model.RequestReturned(err)
			s.mu.Unlock()
		}()
	case BtnCancel:
		if s.Svc == nil {
			return false
		}
		s.mu.Lock()
		s.busy = map[ButtonID]string{BtnCancel: "Cancelling..."}
		s.mu.Unlock()
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_, err := s.Svc.Cancel(ctx)
			s.mu.Lock()
			s.busy = nil
			if err == nil {
				s.model.Cancelled()
			} // else it is past the point of no return: the next reading shows the real step
			s.mu.Unlock()
		}()
	}
	return false
}
