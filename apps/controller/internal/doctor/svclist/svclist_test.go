package svclist

import (
	"context"
	"errors"
	"testing"
)

func TestSplitName(t *testing.T) {
	for in, want := range map[string][3]string{
		"actions.runner.luis-mc-app.crsp-win-1":         {"luis-mc-app", "crsp-win-1", "ok"},
		"actions.runner.myorg.host.example.com.service": {"myorg", "host.example.com", "ok"},
		"actions.runner.myorg":                          {"", "", ""},
		"sshd.service":                                  {"", "", ""},
		"actions.runner..x":                             {"", "", ""},
	} {
		s, r, ok := SplitName(in)
		if (want[2] == "ok") != ok || s != want[0] || r != want[1] {
			t.Errorf("SplitName(%q) = %q %q %v", in, s, r, ok)
		}
	}
}

func TestParseWindows(t *testing.T) {
	out := "actions.runner.luis-mc-app.crsp-win-1|Running|Auto\r\n" +
		"actions.runner.luis-mc-app.crsp-win-2|Stopped|Manual\r\n" +
		"\r\nnoise line\r\n" +
		"Spooler|Running|Auto\r\n"
	got := ParseWindows(out)
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if !got[0].Running || !got[0].Automatic || !got[0].AutomaticKnown || got[0].RunnerName != "crsp-win-1" || got[0].Scope != "luis-mc-app" {
		t.Errorf("first = %+v", got[0])
	}
	if got[1].Running || got[1].Automatic || !got[1].AutomaticKnown {
		t.Errorf("second = %+v", got[1])
	}
}

func TestParseSystemd(t *testing.T) {
	units := "actions.runner.luis-mc-app.crsp-lin-1.service loaded active running GitHub Actions Runner (luis-mc-app.crsp-lin-1)\n" +
		"● actions.runner.luis-mc-app.crsp-lin-2.service loaded failed failed GitHub Actions Runner\n" +
		"cron.service loaded active running Cron\n"
	files := "actions.runner.luis-mc-app.crsp-lin-1.service enabled enabled\n" +
		"actions.runner.luis-mc-app.crsp-lin-2.service disabled enabled\n"
	got := ParseSystemd(units, files)
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if !got[0].Running || !got[0].Automatic || got[0].Name != "actions.runner.luis-mc-app.crsp-lin-1" {
		t.Errorf("first = %+v", got[0])
	}
	if got[1].Running || got[1].Automatic || !got[1].AutomaticKnown {
		t.Errorf("second = %+v", got[1])
	}
	// Without the unit files the enabled state is unknown, not false.
	if got := ParseSystemd(units, ""); got[0].AutomaticKnown {
		t.Errorf("automatic must be unknown: %+v", got[0])
	}
}

func TestParseLaunchd(t *testing.T) {
	out := "PID\tStatus\tLabel\n" +
		"612\t0\tactions.runner.luis-mc-app.crsp-mac-1\n" +
		"-\t0\tactions.runner.luis-mc-app.crsp-mac-2\n" +
		"-\t0\tcom.apple.Finder\n"
	got := ParseLaunchd(out)
	if len(got) != 2 || !got[0].Running || got[1].Running || got[0].AutomaticKnown {
		t.Fatalf("got %+v", got)
	}
}

func TestListPerOS(t *testing.T) {
	var asked []string
	run := func(_ context.Context, name string, _ ...string) ([]byte, error) {
		asked = append(asked, name)
		switch name {
		case "powershell.exe":
			return []byte("actions.runner.o.w|Running|Auto"), nil
		case "systemctl":
			return []byte("actions.runner.o.l.service loaded active running x"), nil
		}
		return []byte("1\t0\tactions.runner.o.m"), nil
	}
	for _, osn := range []string{"windows", "linux", "macos"} {
		got, err := List(context.Background(), osn, run)
		if err != nil || len(got) != 1 {
			t.Errorf("%s: %v %+v", osn, err, got)
		}
	}
	if got, _ := List(context.Background(), "plan9", run); got != nil {
		t.Error("unknown OS must list nothing")
	}
	bad := func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("no systemctl") }
	if _, err := List(context.Background(), "linux", bad); err == nil {
		t.Error("want error")
	}
}
