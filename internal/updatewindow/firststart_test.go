package updatewindow

import (
	"errors"
	"strings"
	"testing"
)

func TestFirstStartStepsGoInOrderAndALaterStepCannotBePressed(t *testing.T) {
	var login, console int
	s := NewFirstStartScreen(FirstStartDeps{Where: "menu bar",
		SetLogin:    func(bool) error { login++; return nil },
		OpenConsole: func() error { console++; return nil }})
	if p := s.Page(); p.State != "welcome" || p.Step != "Step 1 of 3" {
		t.Fatalf("first page = %+v", p)
	}
	if s.Press("save", nil) || s.Press("close", nil) || login+console != 0 {
		t.Fatal("a button of a later step did something")
	}
	s.Press("next", nil)
	if p := s.Page(); p.State != "choices" || len(p.Options) != 2 {
		t.Fatalf("choices page = %+v", p)
	}
	s.Press("back", nil)
	if s.Page().State != "welcome" {
		t.Fatal("Back returns to Welcome")
	}
	s.Press("next", nil)
	s.Press("save", map[string]bool{ChoiceLogin: true, ChoiceConsole: false})
	p := s.Page()
	if p.State != "done" || login != 1 || console != 0 || p.Tone != "done" {
		t.Fatalf("done page = %+v login %d console %d", p, login, console)
	}
	if !strings.Contains(strings.Join(p.Body, " "), WordLoginOn) {
		t.Fatalf("done says %v", p.Body)
	}
	if !s.Press("close", nil) {
		t.Fatal("Close ends the window")
	}
}

func TestFirstStartDefaultsAreBothOnAndNotNowCloses(t *testing.T) {
	s := NewFirstStartScreen(FirstStartDeps{Where: "system tray"})
	s.Press("next", nil)
	for _, o := range s.Page().Options {
		if !o.On {
			t.Errorf("%s is off by default", o.ID)
		}
	}
	s.Press("back", nil)
	if !s.Press("not_now", nil) {
		t.Fatal("Not now closes the window")
	}
}

func TestFirstStartSaysAFailureAndTheWhereWord(t *testing.T) {
	s := NewFirstStartScreen(FirstStartDeps{Where: WhereIsTheIcon("darwin"),
		SetLogin:    func(bool) error { return errors.New("no write access") },
		OpenConsole: func() error { return errors.New("service stopped.") }})
	s.Press("next", nil)
	s.Press("save", map[string]bool{ChoiceLogin: true, ChoiceConsole: true})
	body := strings.Join(s.Page().Body, "\n")
	for _, want := range []string{"menu bar", "Start at login did not change: no write access", "The console did not open: service stopped."} {
		if !strings.Contains(body, want) {
			t.Errorf("done page lacks %q:\n%s", want, body)
		}
	}
	if WhereIsTheIcon("linux") != "system tray" {
		t.Error("Linux says system tray")
	}
}

func TestFirstStartWordsHaveNoBannedWord(t *testing.T) {
	s := NewFirstStartScreen(FirstStartDeps{Where: "menu bar"})
	var all []string
	for i := 0; i < 3; i++ {
		p := s.Page()
		all = append(all, p.Heading)
		all = append(all, p.Body...)
		for _, b := range p.Buttons {
			all = append(all, b.Label)
		}
		for _, o := range p.Options {
			all = append(all, o.Label, o.Hint)
		}
		s.Press("next", nil)
		s.Press("save", nil)
	}
	for _, w := range all {
		for _, bad := range []string{"Dismiss", "Drain", "Yes", "Install now"} {
			if strings.Contains(w, bad) {
				t.Errorf("%q has the retired word %q", w, bad)
			}
		}
	}
}
