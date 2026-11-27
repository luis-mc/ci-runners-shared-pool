package store

import (
	"context"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights/guardrails"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights/workflows"
)

func TestWorkflowChecksStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, err := OpenForTests(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	w := s.WorkflowChecks()
	if sha, rs, err := w.Latest(ctx, 1); err != nil || sha != "" || rs != nil {
		t.Fatalf("empty: %q %v %v", sha, rs, err)
	}
	pr := workflows.File{RepositoryID: 1, HeadSHA: "pr1", Path: ".github/workflows/ci.yml"}
	def := workflows.File{RepositoryID: 1, HeadSHA: "def1", Path: ".github/workflows/ci.yml", DefaultBranch: true}
	res := []workflows.Result{{Check: workflows.CheckTimeout, Job: "a", Status: workflows.Fail, Line: 3, Detail: "x", Guardrail: "G2"}}
	if err := w.Save(ctx, pr, res); err != nil {
		t.Fatal(err)
	}
	if err := w.Save(ctx, def, res); err != nil {
		t.Fatal(err)
	}
	if err := w.Save(ctx, def, res); err != nil { // same key: replaced
		t.Fatal(err)
	}
	sha, rs, err := w.Latest(ctx, 1)
	if err != nil || sha != "def1" || len(rs) != 1 {
		t.Fatalf("%q %+v %v", sha, rs, err)
	}
	path, job := workflows.SplitJob(rs[0].Job)
	if path != ".github/workflows/ci.yml" || job != "a" || rs[0].Line != 3 || rs[0].Guardrail != "G2" || rs[0].Status != workflows.Fail {
		t.Fatalf("%+v", rs[0])
	}
	if rs, err := w.ForCommit(ctx, 1, "pr1"); err != nil || len(rs) != 1 {
		t.Fatalf("%+v %v", rs, err)
	}
}

func TestGuardrailStoreStatus(t *testing.T) {
	ctx := context.Background()
	s, err := OpenForTests(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	for _, q := range []string{
		`INSERT INTO installations (id, account_login, account_type) VALUES (1,'acme','Organization')`,
		`INSERT INTO repositories (id, installation_id, full_name, owner, name) VALUES (10,1,'acme/api','acme','api')`,
	} {
		regExec(t, s, q)
	}
	g := s.GuardrailReports()
	now := time.Now().UTC()
	old := now.Add(-48 * time.Hour)
	own := guardrails.Entry{Guardrail: "G2", State: guardrails.Broken, Count: 1}
	if err := g.SaveOwn(ctx, 10, own, old); err != nil {
		t.Fatal(err)
	}
	rep := guardrails.Report{KnownCount: 7, Entries: []guardrails.Entry{
		{Guardrail: "G2", State: guardrails.Pass},
		{Guardrail: "G1", State: guardrails.Broken, Count: 1, Breaks: []guardrails.Break{{File: "a_test.go", Line: 4, Message: "sleep"}}},
	}}
	if err := g.SaveReport(ctx, 10, 99, "sha", rep, now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	st, err := g.Status(ctx, 0)
	if err != nil || len(st) != 1 || st[0].Repository != "acme/api" || len(st[0].Guardrails) != 2 {
		t.Fatalf("%+v %v", st, err)
	}
	g1, g2 := st[0].Guardrails[0], st[0].Guardrails[1]
	if g1.Guardrail != "G1" || g1.Breaks[0].Message != "sleep" || g2.State != guardrails.Pass || g2.Source != guardrails.FromGuardReport {
		t.Fatalf("%+v %+v", g1, g2)
	}
	if len(st[0].KnownCountByDay) != 1 || st[0].KnownCountByDay[0].Count != 7 {
		t.Fatalf("%+v", st[0].KnownCountByDay)
	}
	if st, err := g.Status(ctx, 11); err != nil || len(st) != 0 {
		t.Fatalf("other repo: %+v %v", st, err)
	}
}
