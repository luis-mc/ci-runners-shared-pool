package configio

import (
	"context"
	"strings"
	"testing"
)

func TestExportHasNoSecretsOrHistory(t *testing.T) {
	e, _, _, _, _ := newEngine()
	e.Secrets = func(context.Context) []SecretRef {
		return []SecretRef{{Name: "GitHub App private key", Ref: "crsp/github/app-1/private-key", State: "set"}}
	}
	b, err := e.Export(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	out := string(b)
	for _, want := range []string{"format: crsp-config", "version: 1", "pool:", "machines:", "name: build-01", "ref: crsp/github/app-1/private-key"} {
		if !strings.Contains(out, want) {
			t.Errorf("export lacks %q:\n%s", want, out)
		}
	}
	for _, bad := range []string{"-----BEGIN", "job_history", "audit_log"} {
		if strings.Contains(out, bad) {
			t.Errorf("export holds %q", bad)
		}
	}
}

func TestPlanRefusesAFileWithASecretValue(t *testing.T) {
	e, _, _, _, _ := newEngine()
	file := "format: crsp-config\nversion: 1\nnotifications:\n  - name: ops-mail\n    smtp_password: hunter2\n"
	p, err := e.Plan(context.Background(), []byte(file))
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "" || len(p.Problems) == 0 || !strings.Contains(p.Problems[0], "smtp_password") {
		t.Fatalf("plan = %+v, want a problem naming smtp_password and no ID", p)
	}
	if strings.Contains(strings.Join(p.Problems, " "), "hunter2") {
		t.Fatal("a problem line repeats the secret value")
	}
}

func TestPlanRefusesANewerVersionAndAWrongHeader(t *testing.T) {
	e, _, _, _, _ := newEngine()
	for _, f := range []string{"format: other\nversion: 1\n", "format: crsp-config\nversion: 2\n", "not: [yaml"} {
		if _, err := e.Plan(context.Background(), []byte(f)); err == nil {
			t.Errorf("file %q was accepted", f)
		}
	}
}
