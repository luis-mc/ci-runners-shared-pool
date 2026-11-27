package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
)

type upStep struct {
	Do        string `json:"do"`
	D         string `json:"d"`
	Offset    string `json:"offset"`
	Up        bool   `json:"up"`
	Accepting bool   `json:"accepting"`
	Reason    string `json:"reason"`
	Action    string `json:"action"`
	Has       bool   `json:"has"`
	Count     int    `json:"count"`
}

type upCase struct {
	Name  string   `json:"name"`
	Steps []upStep `json:"steps"`
}

type upClock struct{ t time.Time }

func (c *upClock) now() time.Time { return c.t }

func addMachine(t *testing.T, repo *machines.MemRepo, id, name string, now time.Time) {
	t.Helper()
	m := machines.Machine{ID: id, Name: name, OS: "linux", Arch: "x64", State: machines.StateActive,
		Online: true, LastSeen: now, JoinedAt: now}
	if err := repo.InsertMachine(context.Background(), m, machines.DefaultSettings(), "hash-"+id); err != nil {
		t.Fatal(err)
	}
}

// github-integration R5.43, machines R5.35: whether a machine counts as up for
// the hosted fallback, from its heartbeat age (the 30 s edge, a heartbeat dated
// in the future beyond the 30 s skew) and its accepting report.
func TestMachineUpEdges(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "machine_up.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []upCase
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			ctx := context.Background()
			clk := &upClock{t: time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)}
			repo := machines.NewMemRepo()
			repo.Now = clk.now
			svc := machines.New(machines.Options{Repo: repo, Now: clk.now})
			addMachine(t, repo, "m1", "one", clk.t)
			for _, st := range c.Steps {
				switch st.Do {
				case "heartbeat":
					if err := svc.MarkSeen(ctx, "m1"); err != nil {
						t.Fatal(err)
					}
				case "advance":
					d, err := time.ParseDuration(st.D)
					if err != nil {
						t.Fatal(err)
					}
					clk.t = clk.t.Add(d)
				case "tick":
					if err := svc.Tick(ctx); err != nil {
						t.Fatal(err)
					}
				case "last_seen":
					d, err := time.ParseDuration(st.Offset)
					if err != nil {
						t.Fatal(err)
					}
					m, err := repo.GetMachine(ctx, "m1")
					if err != nil {
						t.Fatal(err)
					}
					m.Online, m.LastSeen = true, clk.t.Add(d)
					if err := repo.UpdateMachine(ctx, m); err != nil {
						t.Fatal(err)
					}
				case "accepting":
					svc.HandleAccepting(ctx, "m1", &agentv1.AcceptingReport{Accepting: st.Accepting, PauseReason: st.Reason})
				case "second_machine":
					addMachine(t, repo, "m2", "two", clk.t)
				case "expect_up":
					got, err := machineUp{svc: svc}.UpOS(ctx)
					if err != nil {
						t.Fatal(err)
					}
					if got["Linux"] != st.Up {
						t.Fatalf("up = %v, want %v (%v)", got["Linux"], st.Up, got)
					}
				case "expect_audit":
					n := 0
					for _, a := range repo.AuditLog() {
						if a.Action == st.Action {
							n++
						}
					}
					if (n > 0) != st.Has || (st.Count > 0 && n != st.Count) {
						t.Fatalf("audit %q count %d, want has=%v count=%d", st.Action, n, st.Has, st.Count)
					}
				default:
					t.Fatalf("unknown step %q", st.Do)
				}
			}
		})
	}
}
