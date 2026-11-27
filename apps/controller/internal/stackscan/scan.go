package stackscan

import (
	"bytes"
	"container/list"
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
)

// Limits. A file over MaxFileBytes is hashed by its blob SHA (R5.76).
const (
	MaxFileBytes = 1 << 20
	MaxFiles     = 500
	cacheSize    = 64
)

var (
	// ErrBadCommit: the scan reads a commit, never a branch name that moves.
	ErrBadCommit = errors.New("stackscan: the commit must be a full hex sha")
	// ErrTooManyFiles: more stack files than MaxFiles; a partial list would
	// give a wrong stack hash, so the scan stops.
	ErrTooManyFiles = errors.New("stackscan: too many stack files")
	// ErrDelayed: the GitHub rate budget is low (R5.75). Use errors.As with
	// *DelayedError for the time.
	ErrDelayed = errors.New("stackscan: delayed, GitHub rate budget is low")
)

// DelayedError says the scan waits for the rate budget and until when.
type DelayedError struct{ ResetAt time.Time }

func (e *DelayedError) Error() string {
	return "Stack scan delayed until " + e.ResetAt.UTC().Format(time.RFC3339) + ": the GitHub rate budget is low."
}
func (e *DelayedError) Is(t error) bool { return t == ErrDelayed }

// Blob is one file of the commit's tree.
type Blob struct {
	Path string
	SHA  string
	Size int64
}

// Reader is the GitHub side: one tree listing and file reads at a commit.
// repository is owner/name.
type Reader interface {
	Blobs(ctx context.Context, repository, commit string) ([]Blob, error)
	File(ctx context.Context, repository, filePath, commit string) ([]byte, error)
}

// Finding is what the scan keeps about a stack file: never its text.
type Finding struct {
	Path     string
	Kind     Kind
	Size     int64
	BlobSHA  string
	TooLarge bool
}

// Result is one scan.
type Result struct {
	Repository string
	Commit     string
	Files      []registry.StackFile
	Findings   []Finding
	StackHash  string // registry.StackHash(Files)
}

// Scanner implements registry.StackSource.
type Scanner struct {
	r Reader

	mu       sync.Mutex
	cache    map[string]*list.Element
	order    *list.List
	inflight map[string]*call
}

type call struct {
	done chan struct{}
	res  Result
	err  error
}

type entry struct {
	key string
	res Result
}

// New makes a Scanner over r.
func New(r Reader) *Scanner {
	return &Scanner{r: r, cache: map[string]*list.Element{}, order: list.New(), inflight: map[string]*call{}}
}

var _ registry.StackSource = (*Scanner)(nil)

var reCommit = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)

// StackFiles gives the stack files of repository at commit, LF-normalised.
func (s *Scanner) StackFiles(ctx context.Context, repository, commit string) ([]registry.StackFile, error) {
	res, err := s.Scan(ctx, repository, commit)
	if err != nil {
		return nil, err
	}
	return res.Files, nil
}

// Scan reads the stack files at commit. The result is cached by repository and
// commit (a commit never changes), so a second ask costs no GitHub call; a
// failed or delayed scan is not cached. Concurrent asks for one commit share
// one read. The returned slices are the caller's own.
func (s *Scanner) Scan(ctx context.Context, repository, commit string) (Result, error) {
	commit = strings.ToLower(strings.TrimSpace(commit))
	if !reCommit.MatchString(commit) {
		return Result{}, fmt.Errorf("%w: %q", ErrBadCommit, commit)
	}
	key := repository + "@" + commit

	s.mu.Lock()
	if el, ok := s.cache[key]; ok {
		s.order.MoveToFront(el)
		res := clone(el.Value.(*entry).res)
		s.mu.Unlock()
		return res, nil
	}
	if c, ok := s.inflight[key]; ok {
		s.mu.Unlock()
		select {
		case <-c.done:
		case <-ctx.Done():
			return Result{}, ctx.Err()
		}
		if c.err != nil {
			return Result{}, c.err
		}
		return clone(c.res), nil
	}
	c := &call{done: make(chan struct{})}
	s.inflight[key] = c
	s.mu.Unlock()

	c.res, c.err = s.read(ctx, repository, commit)

	s.mu.Lock()
	delete(s.inflight, key)
	if c.err == nil {
		s.cache[key] = s.order.PushFront(&entry{key: key, res: c.res})
		for s.order.Len() > cacheSize {
			last := s.order.Back()
			s.order.Remove(last)
			delete(s.cache, last.Value.(*entry).key)
		}
	}
	s.mu.Unlock()
	close(c.done)
	if c.err != nil {
		return Result{}, c.err
	}
	return clone(c.res), nil
}

func (s *Scanner) read(ctx context.Context, repository, commit string) (Result, error) {
	blobs, err := s.r.Blobs(ctx, repository, commit)
	if err != nil {
		return Result{}, wrapDelay(err)
	}
	type pick struct {
		b Blob
		k Kind
	}
	var picks []pick
	for _, b := range blobs {
		if k, ok := Classify(b.Path); ok {
			picks = append(picks, pick{b, k})
		}
	}
	if len(picks) > MaxFiles {
		return Result{}, fmt.Errorf("%w: %d (limit %d)", ErrTooManyFiles, len(picks), MaxFiles)
	}
	sort.Slice(picks, func(i, j int) bool { return picks[i].b.Path < picks[j].b.Path })

	res := Result{Repository: repository, Commit: commit, Files: []registry.StackFile{}, Findings: []Finding{}}
	for _, p := range picks {
		f := Finding{Path: p.b.Path, Kind: p.k, Size: p.b.Size, BlobSHA: p.b.SHA}
		var content []byte
		if p.b.Size > MaxFileBytes {
			f.TooLarge = true
			content = []byte("git-blob-sha1:" + p.b.SHA)
		} else {
			raw, err := s.r.File(ctx, repository, p.b.Path, commit)
			if err != nil {
				return Result{}, wrapDelay(fmt.Errorf("stackscan: reading %s: %w", p.b.Path, err))
			}
			content = bytes.ReplaceAll(raw, []byte("\r\n"), []byte("\n"))
		}
		res.Files = append(res.Files, registry.StackFile{Path: p.b.Path, Content: content})
		res.Findings = append(res.Findings, f)
	}
	res.StackHash, err = registry.StackHash(res.Files)
	if err != nil {
		return Result{}, err
	}
	return res, nil
}

func wrapDelay(err error) error {
	var rl *github.RateLimitError
	if errors.As(err, &rl) {
		return &DelayedError{ResetAt: rl.ResetAt}
	}
	return err
}

func clone(r Result) Result {
	out := r
	out.Files = make([]registry.StackFile, len(r.Files))
	for i, f := range r.Files {
		out.Files[i] = registry.StackFile{Path: f.Path, Content: append([]byte(nil), f.Content...)}
	}
	out.Findings = append([]Finding(nil), r.Findings...)
	return out
}
