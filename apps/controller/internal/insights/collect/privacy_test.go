package collect

import (
	"context"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/redact"
)

const (
	pem       = "-----BEGIN RSA " + "PRIVATE KEY-----\nMIIEowIBAAKCAQEAverysecretkeymaterial\nsecondlineofkeymaterial\n-----END RSA PRIVATE KEY-----"
	ghToken   = "ghp_" + "abcdefghijklmnopqrstuvwxyz0123456789"
	jwt       = "eyJhbGciOiJIUzI1NiJ9" + ".eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV"
	heldValue = "s3cr3t-held-by-controller"
	envValue  = "env-value-never-kept"
	mail      = "dev.person+ci@example.org"
)

// allText gunzips every stored file and returns the text of the whole CI data folder with the index rows.
func (r *rig) allText(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	err := filepath.WalkDir(r.vault.Dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(r.vault.Dir, p)
		b.WriteString(rel + "\n")
		rc, err := r.vault.Open(context.Background(), filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		defer func() { _ = rc.Close() }()
		body, err := io.ReadAll(rc)
		b.Write(body)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// R5.4: no secret, no env value, no email address reaches the CI data folder, from a log or a report.
func TestNoSecretNoEnvNoEmailStored(t *testing.T) {
	r := newRig(t)
	r.rd.Add(heldValue)
	log := strings.Join([]string{
		"2026-11-20T10:00:00.0000000Z ##[group]Run make",
		"2026-11-20T10:00:00.0000000Z env:",
		"2026-11-20T10:00:00.0000000Z   API_URL: " + envValue,
		"2026-11-20T10:00:00.0000000Z ##[endgroup]",
		"2026-11-20T10:00:01.0000000Z using " + heldValue + " and " + ghToken,
		"2026-11-20T10:00:01.0000000Z Authorization: Bearer abc.def.ghi",
		"2026-11-20T10:00:01.0000000Z author " + mail + " pushed " + jwt,
		pem,
		"2026-11-20T10:00:02.0000000Z done",
	}, "\n") + "\n"
	r.gh.logs[1] = log
	report := `<testsuite name="s"><testcase name="login for ` + mail + `"><system-out>` + ghToken + " " + heldValue + `</system-out></testcase></testsuite>`
	r.gh.artifacts = []Artifact{{ID: 5, Name: "r", Size: 10}}
	r.gh.zips[5] = zipOf(t, map[string]string{"junit.xml": report})
	r.svc.JobCompleted(context.Background(), 3, 900, 1, 1)
	r.svc.RunCompleted(context.Background(), 3, 900, 1)
	r.drain(t)

	text := r.allText(t)
	for _, leaked := range []string{heldValue, envValue, mail, ghToken, jwt, "very" + "secretkeymaterial", "secondlineofkeymaterial", "Bearer abc"} {
		if strings.Contains(text, leaked) {
			t.Errorf("stored text holds %q", leaked)
		}
	}
	for _, kept := range []string{"API_URL: ***", "done", "<testsuite", "login for ***"} {
		if !strings.Contains(text, kept) {
			t.Errorf("stored text lost %q", kept)
		}
	}
	// the index rows hold paths, sizes, hashes and fixed words only
	row, _, _ := r.db.CIJobLogOf(context.Background(), 1)
	rows := row.Path + row.Reason + row.Sha256 + row.State
	if strings.Contains(rows, heldValue) || strings.Contains(rows, mail) {
		t.Errorf("row = %+v", row)
	}
}

// A report on one line longer than the chunk the redactor sees is cut at a separator: a token that sits on
// the cut is not split in two.
func TestSecretOnTheChunkBoundary(t *testing.T) {
	defer func(n int) { maxChunk = n }(maxChunk)
	maxChunk = 4096
	v := &FSVault{Dir: t.TempDir(), Redactor: redact.New()}
	filler := strings.Repeat("word ", (maxChunk-20)/5)
	long := `{"a":"` + filler + ghToken + ` ` + filler + `"}`
	if i := strings.Index(long, ghToken); i > maxChunk || i+len(ghToken) < maxChunk {
		t.Fatalf("the token must cross the cut: at %d", i)
	}
	st, err := v.PutReport(context.Background(), Key{RepositoryID: 1, RunID: 1, RunAttempt: 1, ArtifactID: 1, Member: "r.json"}, strings.NewReader(long))
	if err != nil {
		t.Fatal(err)
	}
	rc, err := v.Open(context.Background(), st.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	body, _ := io.ReadAll(rc)
	if text := string(body); strings.Contains(text, "ghp_") || !strings.HasSuffix(text, `"}`) {
		t.Fatalf("token left or text cut (%d bytes)", len(text))
	}
}

// R6.5: every shape and every value the redactor knows is masked by both sinks of the CI data folder.
func TestRedactionCoversCIData(t *testing.T) {
	r := newRig(t)
	r.rd.Add(heldValue)
	secrets := []string{
		ghToken, "github_pat_" + strings.Repeat("A1b2C3d4_", 4), pem, "crsp_cli_abcdefgh12345", "crsp_acc_abcdefgh12345", jwt,
		"Authorization: Bearer topsecretvalue", "cookie: session=abc123def", "--token mysecrettoken", "ACTIONS_RUNNER_INPUT_JITCONFIG=abcdef", heldValue,
	}
	for i, s := range secrets {
		body := "before\n" + s + "\nafter\n"
		lg, err := r.vault.PutLog(context.Background(), Key{RepositoryID: 1, RunID: 1, RunAttempt: 1, JobID: int64(i)}, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		rp, err := r.vault.PutReport(context.Background(), Key{RepositoryID: 1, RunID: 1, RunAttempt: 1, ArtifactID: int64(i), Member: "r.txt"}, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range []string{lg.Path, rp.Path} {
			text := r.readLog(t, p)
			core := strings.Fields(s)[len(strings.Fields(s))-1]
			if strings.Contains(text, core) || !strings.Contains(text, "before") || !strings.Contains(text, "after") {
				t.Errorf("secret %d in %s: %q", i, p, text)
			}
		}
	}
	// a missing redactor stores nothing
	empty := &FSVault{Dir: t.TempDir()}
	if _, err := empty.PutLog(context.Background(), Key{RepositoryID: 1}, strings.NewReader("x")); err != ErrRedactor {
		t.Fatalf("no redactor: %v", err)
	}
}

// R6.5: no insights package makes a network call of its own: GitHub goes through internal/github, the agent
// through the agent link.
func TestNoNetworkImportInInsights(t *testing.T) {
	root := ".."
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return err
		}
		f, err := parser.ParseFile(fset, p, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, im := range f.Imports {
			if path := strings.Trim(im.Path.Value, `"`); path == "net/http" || path == "net" {
				t.Errorf("%s imports %s", p, path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatal(err)
	}
}
