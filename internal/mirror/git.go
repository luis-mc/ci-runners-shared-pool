package mirror

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/backend/sys"
)

// Status of a mirror (UX-58 words: ok, waiting, failed).
const (
	StatusOK      = "ok"
	StatusWaiting = "waiting"
	StatusFailed  = "failed"
)

// ErrBadRepo is a repository name that is not owner/repo.
var ErrBadRepo = errors.New("a repository is named owner/repo with letters, digits, - _ and .")

var repoRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9_.-]+$`)

// ValidRepo says whether name is owner/repo and cannot leave the git folder.
func ValidRepo(name string) bool {
	if !repoRe.MatchString(name) || strings.Contains(name, "..") || strings.HasSuffix(name, ".git") {
		return false
	}
	return true
}

func folderName(repo string) string { return strings.Replace(repo, "/", "__", 1) + ".git" }

// MirrorState is what the controller knows of one mirror.
type MirrorState struct {
	Repo        string
	Path        string
	Size        int64
	LastRefresh time.Time
	Status      string
	Error       string
}

// Git keeps the bare mirrors (caches R5.67). Commands go through Run.
type Git struct {
	Dir     string // the git folder of the layout
	Run     sys.Commands
	Now     func() time.Time
	Timeout time.Duration
	// GitHub is the base address of the upstream, "https://github.com" by default.
	GitHub string

	mu    sync.Mutex
	state map[string]*MirrorState
	locks map[string]*sync.Mutex
}

func (g *Git) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

func (g *Git) github() string {
	if g.GitHub != "" {
		return strings.TrimRight(g.GitHub, "/")
	}
	return "https://github.com"
}

func (g *Git) timeout() time.Duration {
	if g.Timeout > 0 {
		return g.Timeout
	}
	return 10 * time.Minute
}

// Path is the folder of a repository's mirror.
func (g *Git) Path(repo string) (string, error) {
	if !ValidRepo(repo) {
		return "", ErrBadRepo
	}
	return filepath.Join(g.Dir, folderName(repo)), nil
}

func (g *Git) lock(repo string) *sync.Mutex {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.locks == nil {
		g.locks = map[string]*sync.Mutex{}
		g.state = map[string]*MirrorState{}
	}
	l, ok := g.locks[repo]
	if !ok {
		l = &sync.Mutex{}
		g.locks[repo] = l
	}
	return l
}

func (g *Git) set(repo string, f func(*MirrorState)) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state == nil {
		g.state = map[string]*MirrorState{}
	}
	st := g.state[repo]
	if st == nil {
		st = &MirrorState{Repo: repo, Status: StatusWaiting}
		g.state[repo] = st
	}
	f(st)
}

func (g *Git) git(ctx context.Context, dir string, args ...string) (sys.Result, error) {
	ctx, cancel := context.WithTimeout(ctx, g.timeout())
	defer cancel()
	res, err := g.Run.Run(ctx, sys.Command{Name: "git", Args: args, Dir: dir, Env: map[string]string{"GIT_TERMINAL_PROMPT": "0"}})
	if err != nil {
		return res, err
	}
	if res.ExitCode != 0 {
		return res, fmt.Errorf("git %s: %s", args[0], firstLine(res.Stderr))
	}
	return res, nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return "no message"
	}
	return s
}

// Ensure makes the bare mirror of a repository when it is not there.
func (g *Git) Ensure(ctx context.Context, repo string) (string, error) {
	p, err := g.Path(repo)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(p, "HEAD")); err == nil {
		return p, nil
	}
	if err := os.MkdirAll(g.Dir, 0o755); err != nil {
		return "", err
	}
	if _, err := g.git(ctx, "", "init", "--bare", p); err != nil {
		return "", err
	}
	url := g.github() + "/" + repo + ".git"
	for _, a := range [][]string{
		{"remote", "add", "origin", url},
		{"config", "--add", "remote.origin.fetch", "+refs/*:refs/*"},
		{"config", "--add", "remote.origin.fetch", "+refs/pull/*:refs/pull/*"},
	} {
		if _, err := g.git(ctx, p, a...); err != nil {
			_ = os.RemoveAll(p)
			return "", err
		}
	}
	g.set(repo, func(s *MirrorState) { s.Path = p })
	return p, nil
}

// Refresh fetches every branch, tag and pull request ref from GitHub. One
// refresh of a repository runs at a time; a failed refresh keeps the old
// content and marks the mirror failed (R6.7).
func (g *Git) Refresh(ctx context.Context, repo string) error {
	l := g.lock(repo)
	l.Lock()
	defer l.Unlock()
	p, err := g.Ensure(ctx, repo)
	if err != nil {
		g.set(repo, func(s *MirrorState) { s.Status, s.Error = StatusFailed, err.Error() })
		return err
	}
	if _, err := g.git(ctx, p, "fetch", "--prune", "--tags", "--force", "origin"); err != nil {
		g.set(repo, func(s *MirrorState) { s.Path, s.Status, s.Error = p, StatusFailed, err.Error() })
		return err
	}
	size := dirSize(p)
	now := g.now()
	g.set(repo, func(s *MirrorState) { s.Path, s.Size, s.LastRefresh, s.Status, s.Error = p, size, now, StatusOK, "" })
	return nil
}

// Has says whether the mirror holds a commit or ref.
func (g *Git) Has(ctx context.Context, repo, rev string) bool {
	p, err := g.Path(repo)
	if err != nil || strings.HasPrefix(rev, "-") {
		return false
	}
	if _, err := os.Stat(filepath.Join(p, "HEAD")); err != nil {
		return false
	}
	_, err = g.git(ctx, p, "cat-file", "-e", rev+"^{commit}")
	return err == nil
}

// Resolution is where a job's fetch goes.
type Resolution struct {
	FromMirror bool
	Refreshed  bool
	Reason     string
}

// Resolve answers from the mirror when it has the revision; otherwise it
// refreshes first (R5.13); when the refresh fails the job goes to GitHub.
func (g *Git) Resolve(ctx context.Context, repo, rev string) Resolution {
	if !ValidRepo(repo) {
		return Resolution{Reason: ErrBadRepo.Error()}
	}
	if g.Has(ctx, repo, rev) {
		return Resolution{FromMirror: true}
	}
	if err := g.Refresh(ctx, repo); err != nil {
		return Resolution{Reason: "the mirror could not refresh, so GitHub answers: " + err.Error()}
	}
	if g.Has(ctx, repo, rev) {
		return Resolution{FromMirror: true, Refreshed: true}
	}
	return Resolution{Refreshed: true, Reason: "GitHub does not have it either; the job asks GitHub"}
}

// Rewrite is the two git config pairs of a job (R5.12): fetch from the
// mirror, push to GitHub. mirrorAddr is the base address the job reaches the
// mirror at, for example https://10.0.0.5:8443/git.
func (g *Git) Rewrite(repo, mirrorAddr string) ([][2]string, error) {
	if !ValidRepo(repo) {
		return nil, ErrBadRepo
	}
	up := g.github() + "/" + repo
	mir := strings.TrimRight(mirrorAddr, "/") + "/" + folderName(repo)
	return [][2]string{
		{"url." + mir + ".insteadOf", up},
		{"url." + up + ".pushInsteadOf", up},
	}, nil
}

// States lists every known mirror, newest data from the folder on disk.
func (g *Git) States() []MirrorState {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]MirrorState, 0, len(g.state))
	for _, s := range g.state {
		c := *s
		if c.Path != "" {
			c.Size = dirSize(c.Path)
		}
		out = append(out, c)
	}
	sortStates(out)
	return out
}

// Drop deletes one mirror folder and forgets its state.
func (g *Git) Drop(repo string) (int64, error) {
	p, err := g.Path(repo)
	if err != nil {
		return 0, err
	}
	l := g.lock(repo)
	l.Lock()
	defer l.Unlock()
	n := dirSize(p)
	if err := os.RemoveAll(p); err != nil {
		return 0, err
	}
	g.mu.Lock()
	delete(g.state, repo)
	g.mu.Unlock()
	return n, nil
}

func dirSize(p string) int64 {
	var n int64
	_ = filepath.WalkDir(p, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if i, e := d.Info(); e == nil {
			n += i.Size()
		}
		return nil
	})
	return n
}
