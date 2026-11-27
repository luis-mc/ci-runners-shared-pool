// Package persistent holds what the three persistent backends share: the
// runner download with its pinned hash, the .env and meta files, and the rerun
// decision (runners R5.20, R5.37).
package persistent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
)

// MetaFile is the file CRSP keeps in each runner folder. It has no secret.
const MetaFile = ".crsp.json"

// Meta describes what CRSP made, for reruns and for the managed check.
type Meta struct {
	Name          string   `json:"name"`
	Account       string   `json:"account"`
	Folder        string   `json:"folder"`
	Service       string   `json:"service"`
	GitHubURL     string   `json:"github_url"`
	Labels        []string `json:"labels"`
	SymlinkRight  bool     `json:"symlink_right"`
	RunnerVersion string   `json:"runner_version"`
}

// Normalize sorts the labels so two metas compare.
func (m Meta) Normalize() Meta {
	l := append([]string(nil), m.Labels...)
	sort.Strings(l)
	m.Labels = l
	return m
}

// Action is what a rerun does (runners R5.20).
type Action string

// Rerun actions.
const (
	Fresh        Action = "fresh"
	Already      Action = "already"
	Reregister   Action = "reregister"
	UpdateRights Action = "update-rights"
	RewriteEnv   Action = "rewrite-env"
)

// Decide compares what is on the machine with what is wanted.
//
// Same name, folder, labels, account, rights and .env: already, untouched.
// Another account (or labels, or GitHub URL): register again in place. Only
// the symlink right differs: change the right. Only a stale .env: rewrite it
// and restart, with no new password.
func Decide(have *Meta, haveEnv string, want Meta, wantEnv string) Action {
	if have == nil {
		return Fresh
	}
	h, w := have.Normalize(), want.Normalize()
	if h.Account != w.Account || h.Folder != w.Folder || h.GitHubURL != w.GitHubURL ||
		strings.Join(h.Labels, ",") != strings.Join(w.Labels, ",") || h.Name != w.Name {
		return Reregister
	}
	if h.SymlinkRight != w.SymlinkRight {
		return UpdateRights
	}
	if haveEnv != wantEnv {
		return RewriteEnv
	}
	return Already
}

// ReadMeta reads the meta file at file. It returns nil with no error when the
// file does not exist (a runner CRSP did not make) and an error when the file
// cannot be read, such as for lack of rights (runners R5.5).
func ReadMeta(fsys sys.FS, file string) (*Meta, error) {
	b, err := fsys.ReadFile(file)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var m Meta
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("read %s: %w", MetaFile, err)
	}
	return &m, nil
}

// WriteMeta writes the meta file at file.
func WriteMeta(fsys sys.FS, file string, m Meta) error {
	b, err := json.MarshalIndent(m.Normalize(), "", "  ")
	if err != nil {
		return err
	}
	return fsys.WriteFile(file, b, 0o600)
}

// Env renders a runner .env: sorted KEY=value lines.
func Env(kv map[string]string) string {
	keys := make([]string, 0, len(kv))
	for k := range kv {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k + "=" + kv[k] + "\n")
	}
	return b.String()
}

// Download fetches the pinned runner archive and checks its SHA-256. A file
// that does not match is deleted and refused (runners R5.37).
func Download(ctx context.Context, d sys.Deps, url, dest, sha string) error {
	if d.Fetch == nil || d.Verify == nil {
		return errors.New("no downloader configured")
	}
	// A wrong hash may come from a cut or corrupted transfer: the file is
	// deleted and fetched once more. A second wrong hash is refused (runners
	// R5.37). A pin that is not a real hash is never retried.
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		if err = d.Fetch.Fetch(ctx, url, dest); err != nil {
			return fmt.Errorf("download runner: %w", err)
		}
		if err = d.Verify(dest, sha); err == nil {
			return nil
		}
		_ = d.FS.RemoveAll(dest)
		if !errors.Is(err, runners.ErrHashMismatch) {
			break
		}
	}
	return fmt.Errorf("runner download refused: %w", err)
}

// ScopeSlug turns a GitHub URL into the service-name part owner[-repo].
func ScopeSlug(githubURL string) string {
	u := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(githubURL, "https://"), "http://"), "/")
	u = strings.TrimPrefix(u, "github.com/")
	return strings.ReplaceAll(u, "/", "-")
}

// ServiceName is the service entry name: actions.runner.<scope>.<name>, the
// name the runner's own tooling uses and the service list is searched for
// (runners R5.5).
func ServiceName(githubURL, runnerName string) string {
	return "actions.runner." + ScopeSlug(githubURL) + "." + runnerName
}

// ConfigArgs are the config.sh / config.cmd arguments to register a runner,
// one per element. The token goes in the environment, not in an argument.
// The runner's own self-update is off (runners R5.38).
func ConfigArgs(githubURL, name, group string, labels []string) []string {
	a := []string{"--unattended", "--url", githubURL, "--name", name, "--labels", strings.Join(labels, ","),
		"--disableupdate", "--replace"}
	if group != "" {
		a = append(a, "--runnergroup", group)
	}
	return a
}
