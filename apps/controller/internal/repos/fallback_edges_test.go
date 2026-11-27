package repos

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github/fake"
)

// edgeStep is one line of a fixture in testdata/fallback_edges.json.
type edgeStep struct {
	Do      string `json:"do"`
	OS      string `json:"os"`
	Up      bool   `json:"up"`
	D       string `json:"d"`
	At      string `json:"at"`
	ID      int64  `json:"id"`
	Label   string `json:"label"`
	Var     string `json:"var"`
	Where   string `json:"where"`
	Why     string `json:"why"`
	Action  string `json:"action"`
	Has     bool   `json:"has"`
	Minutes int    `json:"minutes"`
}

type edgeCase struct {
	Name  string     `json:"name"`
	OptIn bool       `json:"opt_in"`
	Cap   int        `json:"cap"`
	Steps []edgeStep `json:"steps"`
}

func loadEdgeCases(t *testing.T) []edgeCase {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "fallback_edges.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cs []edgeCase
	if err := json.Unmarshal(raw, &cs); err != nil {
		t.Fatal(err)
	}
	if len(cs) == 0 {
		t.Fatal("no fixtures")
	}
	return cs
}

// R5.42, R5.43, R5.44, R5.45: the edges of the hosted fallback, driven by a
// table of fixtures (the grace boundary, flapping, the cap, the month
// rollover, an opt-out or a public repository while on fallback).
func TestFallbackEdges(t *testing.T) {
	for _, c := range loadEdgeCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			e, up := fallbackEnv(t)
			if c.OptIn {
				optIn(t, e, 101, c.Cap)
			}
			for i, st := range c.Steps {
				runEdgeStep(t, e, up, st, i)
			}
		})
	}
}

func runEdgeStep(t *testing.T, e *env, up *upSrc, st edgeStep, i int) {
	t.Helper()
	ctx := context.Background()
	at := func(msg string) string { return "step " + string(rune('0'+i%10)) + " " + st.Do + ": " + msg }
	switch st.Do {
	case "up":
		up.m[st.OS] = st.Up
	case "no_machine":
		delete(up.m, st.OS)
	case "advance":
		d, err := time.ParseDuration(st.D)
		if err != nil {
			t.Fatal(err)
		}
		e.advance(d)
	case "advance_to":
		to, err := time.Parse(time.RFC3339, st.At)
		if err != nil {
			t.Fatal(err)
		}
		e.mu.Lock()
		e.now = to
		e.mu.Unlock()
	case "eval":
		if err := e.svc.EvaluateFallback(ctx); err != nil {
			t.Fatal(at(err.Error()))
		}
	case "optin":
		optIn(t, e, 101, 0)
	case "optout":
		off := false
		if _, err := e.svc.UpdatePolicy(ctx, adminP, 101, PolicyUpdate{HostedFallback: &off}); err != nil {
			t.Fatal(err)
		}
	case "make_public":
		r, err := e.store.GetRepository(ctx, 101)
		if err != nil {
			t.Fatal(err)
		}
		r.Visibility = "public"
		if err := e.store.UpsertRepository(ctx, r); err != nil {
			t.Fatal(err)
		}
	case "job":
		e.fake.AddJob(fake.Job{ID: st.ID, RunID: st.ID * 10, Repo: "alice/secret", Name: "build", Status: "in_progress", Event: "push", Labels: []string{st.Label}})
	case "job_ends":
		for _, j := range e.fake.Jobs {
			if j.ID == st.ID {
				j.Status = "completed"
			}
		}
	case "expect_job_running":
		for _, j := range e.fake.Jobs {
			if j.ID == st.ID && j.Status != "in_progress" {
				t.Fatal(at("a running hosted job is never stopped"))
			}
		}
	case "expect":
		want := crspLinux
		if st.Var == "hosted" {
			want = `["ubuntu-latest"]`
		}
		if st.Var == "crsp_public" { // a public repository defaults to ephemeral only (R5.60)
			want = `["self-hosted","linux","x64","docker","crsp-home","ephemeral"]`
		}
		if got := linuxVar(e); got != want {
			t.Fatalf("%s: variable %s, want %s", at(""), got, want)
		}
		rt, err := e.svc.RoutingOf(ctx, 101)
		if err != nil {
			t.Fatal(err)
		}
		if rt.Where != st.Where || rt.Why != st.Why {
			t.Fatalf("%s: routing %+v, want %s/%s", at(""), rt, st.Where, st.Why)
		}
	case "expect_used":
		if used, _ := e.svc.FallbackUsedMinutes(ctx, 101, e.clock()); used != st.Minutes {
			t.Fatalf("%s: used %d, want %d", at(""), used, st.Minutes)
		}
	case "expect_audit":
		if got := e.audit.has(st.Action); got != st.Has {
			t.Fatalf("%s: audit %q is %v, want %v (%s)", at(""), st.Action, got, st.Has, strings.Join(e.audit.events, "; "))
		}
	default:
		t.Fatalf("unknown step %q", st.Do)
	}
}
