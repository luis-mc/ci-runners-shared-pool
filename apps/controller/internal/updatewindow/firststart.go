package updatewindow

import (
	"context"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/tray"
)

// The first-start window (TRAY7, decision tray D20): Welcome, choices, Done, shown once on
// macOS and Linux. Steps are in order and a later step cannot be pressed (UX-30): each step
// shows only its own buttons.

// FirstStep is one step of the first-start window.
type FirstStep int

// The three steps.
const (
	StepWelcome FirstStep = iota
	StepChoices
	StepDone
)

// The buttons and choices of the first-start window.
const (
	BtnNext       ButtonID = "next"
	BtnBack       ButtonID = "back"
	BtnSave       ButtonID = "save"
	ChoiceLogin            = "login"
	ChoiceConsole          = "console"
)

// The words of the first-start window; they are in the design system's content list
// ("First-start window words").
const (
	FirstTitle         = "Welcome - CI Runners Shared Pool"
	WordWelcome        = "Welcome to CI Runners Shared Pool"
	WordWelcomeBody    = "This computer can now run the jobs of your GitHub repositories. A small icon in the %s shows how it is doing."
	WordWelcomeBody2   = "Two quick choices, then you are done."
	WordChoicesTitle   = "Your choices"
	WordChoiceLogin    = "Start at login"
	WordChoiceLoginH   = "The icon comes back every time you sign in."
	WordChoiceConsole  = "Open the console"
	WordChoiceConsoleH = "Sign in and set up your pool in the browser."
	WordNext           = "Next"
	WordBack           = "Back"
	WordSaveChoices    = "Save choices"
	WordAllSet         = "All set"
	WordAllSetBody     = "Click the icon in the %s for the menu. Look for updates is there too."
	WordLoginOn        = "Start at login is on."
	WordLoginOff       = "Start at login is off."
	WordLoginFailed    = "Start at login did not change: %s"
	WordConsoleOpened  = "The console is opening in your browser."
	WordConsoleFailed  = "The console did not open: %s Use Open console in the menu."
	WordMenuBar        = "menu bar"
	WordTrayArea       = "system tray"
)

// FirstStartDeps is what the window does for the choices; tests replace each part.
type FirstStartDeps struct {
	SetLogin    func(on bool) error
	OpenConsole func() error
	Where       string // "menu bar" or "system tray"
}

// FirstStartScreen is the first-start window as a web page.
type FirstStartScreen struct {
	Deps FirstStartDeps

	mu      sync.Mutex
	step    FirstStep
	login   bool
	console bool
	result  []string
}

// NewFirstStartScreen starts on Welcome with both choices on.
func NewFirstStartScreen(d FirstStartDeps) *FirstStartScreen {
	if d.Where == "" {
		d.Where = WhereIsTheIcon(runtime.GOOS)
	}
	return &FirstStartScreen{Deps: d, login: true, console: true}
}

// WhereIsTheIcon is where the person looks for the icon.
func WhereIsTheIcon(goos string) string {
	if goos == "darwin" {
		return WordMenuBar
	}
	return WordTrayArea
}

// Name is the one-window name.
func (*FirstStartScreen) Name() string { return "first-start-window" }

// Title is the title of the window.
func (*FirstStartScreen) Title() string { return FirstTitle }

// Size is the size of the window.
func (*FirstStartScreen) Size() (int, int) { return FirstStartWidth, FirstStartHeight }

// Tick has nothing to read.
func (*FirstStartScreen) Tick(context.Context) {}

// Page is the picture of the step now.
func (s *FirstStartScreen) Page() PageView {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.view()
}

func (s *FirstStartScreen) view() PageView {
	v := PageView{Bar: "none", Percent: -1, Tone: "normal"}
	switch s.step {
	case StepWelcome:
		v.State, v.Step, v.Heading = "welcome", "Step 1 of 3", WordWelcome
		v.Body = []string{sprintf(WordWelcomeBody, s.Deps.Where), WordWelcomeBody2}
		v.Buttons = []PageButton{{ID: string(BtnNotNow), Label: WordNotNow}, {ID: string(BtnNext), Label: WordNext, Primary: true}}
		v.Default, v.Escape = string(BtnNext), string(BtnNotNow)
	case StepChoices:
		v.State, v.Step, v.Heading = "choices", "Step 2 of 3", WordChoicesTitle
		v.Options = []PageOption{
			{ID: ChoiceLogin, Label: WordChoiceLogin, Hint: WordChoiceLoginH, On: s.login},
			{ID: ChoiceConsole, Label: WordChoiceConsole, Hint: WordChoiceConsoleH, On: s.console},
		}
		v.Buttons = []PageButton{{ID: string(BtnBack), Label: WordBack}, {ID: string(BtnSave), Label: WordSaveChoices, Primary: true}}
		v.Default, v.Escape = string(BtnSave), string(BtnBack)
	default:
		v.State, v.Step, v.Heading, v.Tone = "done", "Step 3 of 3", WordAllSet, "done"
		v.Body = append([]string{sprintf(WordAllSetBody, s.Deps.Where)}, s.result...)
		v.Buttons = []PageButton{{ID: string(BtnClose), Label: WordClose, Primary: true}}
		v.Default, v.Escape = string(BtnClose), string(BtnClose)
	}
	return v
}

// Press moves between the steps. A button the step does not show does nothing.
func (s *FirstStartScreen) Press(id string, on map[string]bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	shown := false
	for _, b := range s.view().Buttons {
		if b.ID == id && !b.Disabled {
			shown = true
		}
	}
	if !shown {
		return false
	}
	switch ButtonID(id) {
	case BtnNotNow, BtnClose:
		return true
	case BtnNext:
		s.step = StepChoices
	case BtnBack:
		s.step = StepWelcome
	case BtnSave:
		if v, ok := on[ChoiceLogin]; ok {
			s.login = v
		}
		if v, ok := on[ChoiceConsole]; ok {
			s.console = v
		}
		s.result = s.apply()
		s.step = StepDone
	}
	return false
}

// apply does the two choices and says each result in a line.
func (s *FirstStartScreen) apply() []string {
	var out []string
	if s.Deps.SetLogin != nil {
		if err := s.Deps.SetLogin(s.login); err != nil {
			out = append(out, sprintf(WordLoginFailed, err.Error()))
		} else if s.login {
			out = append(out, WordLoginOn)
		} else {
			out = append(out, WordLoginOff)
		}
	}
	if s.console && s.Deps.OpenConsole != nil {
		if err := s.Deps.OpenConsole(); err != nil {
			out = append(out, sprintf(WordConsoleFailed, err.Error()))
		} else {
			out = append(out, WordConsoleOpened)
		}
	}
	return out
}

// RunFirstStart is `crsp first-start`: it marks the window shown, then opens it.
func RunFirstStart(o Options) int {
	exe, _ := os.Executable()
	client := tray.NewClient()
	deps := FirstStartDeps{
		SetLogin: func(on bool) error {
			a, err := tray.DefaultAutostart(exe)
			if err != nil {
				return err
			}
			return a.Set(on)
		},
		OpenConsole: func() error {
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			url, err := client.Link(ctx)
			if err != nil {
				return err
			}
			return tray.OSLauncher(url)
		},
	}
	if !HasBrowser() {
		say(o.Stderr, "error: no Edge, Chrome or Chromium found for the window")
		return 1
	}
	_ = tray.MarkFirstStartShown() // shown once, even when it is closed at once
	return RunWeb(NewFirstStartScreen(deps), o)
}
