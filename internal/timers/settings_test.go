package timers

import (
	"reflect"
	"testing"
)

func ip(n int) *int { return &n }

// R5.103: the owner's defaults.
func TestDefaults(t *testing.T) {
	d := Defaults()
	if d.JobMin != 30 || d.RunMin != 45 || d.HoldMin != 2 || d.TimeoutMin != 60 || d.QueuedMin != 720 || d.StartupMin != 2 {
		t.Fatalf("%+v", d)
	}
	if len(d.Validate()) != 0 {
		t.Fatalf("defaults refused: %v", d.Validate())
	}
}

// R5.108: whole minutes above 0, the hold within its cap, job below run.
func TestSettingsValidation(t *testing.T) {
	cases := []struct {
		name string
		in   Input
		want []Problem
	}{
		{"fine", Input{JobMin: ip(20), RunMin: ip(50)}, nil},
		{"zero job", Input{JobMin: ip(0)}, []Problem{{FieldJob, MsgWhole}}},
		{"negative run", Input{RunMin: ip(-5)}, []Problem{{FieldRun, MsgWhole}}},
		{"job equals run", Input{JobMin: ip(45)}, []Problem{{FieldJob, MsgJobRun}}},
		{"job above run", Input{JobMin: ip(50)}, []Problem{{FieldJob, MsgJobRun}}},
		{"hold above cap", Input{HoldMin: ip(3)}, []Problem{{FieldHold, MsgHoldMax}}},
		{"zero timeout", Input{TimeoutMin: ip(0)}, []Problem{{FieldTimeout, MsgWhole}}},
		{"zero queued", Input{QueuedMin: ip(0)}, []Problem{{FieldQueued, MsgWhole}}},
		{"zero startup", Input{StartupMin: ip(0)}, []Problem{{FieldStartup, MsgWhole}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, got := Check(Defaults(), c.in)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("got %v want %v", got, c.want)
			}
		})
	}
}

// R5.108: a saved set that does not validate never reaches a timer.
func TestParseKeepsDefaultsForABadSet(t *testing.T) {
	if got := Parse(""); got != Defaults() {
		t.Fatalf("empty %+v", got)
	}
	s := Defaults()
	s.JobMin, s.RunMin = 10, 20
	if got := Parse(s.Encode()); got != s {
		t.Fatalf("round trip %+v", got)
	}
	if got := Parse(`{"job_minutes": 50, "run_minutes": 40}`); got != Defaults() {
		t.Fatalf("job above run %+v", got)
	}
	if got := Parse(`not json`); got != Defaults() {
		t.Fatalf("junk %+v", got)
	}
}

// R5.103: the .crsp.yml override per job.
func TestFileLimits(t *testing.T) {
	f := ParseFile([]byte("sizes: {a: heavy}\ntimers:\n  jobs:\n    integration-*: 40\n    smoke: 5\n    bad: 0\n    worse: soon\n"))
	if len(f.Errors) != 2 {
		t.Fatalf("errors %v", f.Errors)
	}
	set := Defaults()
	if n, why := LimitFor(set, f, "integration-db"); n != 40 || why != "" {
		t.Fatalf("glob %d %q", n, why)
	}
	if n, _ := LimitFor(set, f, "smoke"); n != 5 {
		t.Fatalf("exact %d", n)
	}
	if n, _ := LimitFor(set, f, "build"); n != 30 {
		t.Fatalf("default %d", n)
	}
	set.RunMin = 40
	if n, why := LimitFor(set, f, "integration-db"); n != 30 || why == "" {
		t.Fatalf("above run %d %q", n, why)
	}
	if f := ParseFile([]byte("a: [")); len(f.Errors) != 1 {
		t.Fatal("bad yaml not named")
	}
}
