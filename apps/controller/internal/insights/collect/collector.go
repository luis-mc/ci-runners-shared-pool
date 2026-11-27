package collect

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
)

// Repo is what the collector needs to call GitHub for one repository.
type Repo struct {
	Owner, Name    string
	InstallationID int64
}

// Repositories names a repository by its id.
type Repositories interface {
	Repository(ctx context.Context, id int64) (Repo, error)
}

// Index keeps the rows of stored files (ci_job_logs, ci_report_files). *store.Store implements it.
type Index interface {
	CIJobLogOf(ctx context.Context, jobID int64) (store.CIJobLog, bool, error)
	SaveCIJobLog(ctx context.Context, l store.CIJobLog) error
	CIReportFileOf(ctx context.Context, repositoryID, artifactID int64, member string) (store.CIReportFile, bool, error)
	SaveCIReportFile(ctx context.Context, f store.CIReportFile) (int64, error)
}

// ReportRecord is a report file the collector stored; the sink parses it (insights/tests, insights/guardrails).
type ReportRecord struct {
	ID           int64
	RepositoryID int64
	RunID        int64
	RunAttempt   int
	ArtifactID   int64
	Member       string
	Format       string
	Path         string // for Vault.Open
}

// Sink takes a stored report file and parses it into test_results or guardrail_reports. An error marks the
// file broken (R6.8: nothing else stops).
type Sink interface {
	ReportStored(ctx context.Context, r ReportRecord) error
}

// Config builds a Service.
type Config struct {
	GitHub GitHub
	Vault  Vault
	Index  Index
	Repos  Repositories
	Budget Budget
	// Sink parses stored reports; nil leaves them stored.
	Sink Sink
	// Limits reads the settings of R5.5 each time work starts, so a change counts at once.
	Limits func(ctx context.Context) (Limits, error)
	// Detectors default to DefaultDetectors.
	Detectors []Detector
	// GuardrailArtifact is the name pattern of the guard report (setting insights_guardrail_artifact); its
	// artifacts are read first. The content decides, never the name.
	GuardrailArtifact string
	// TempDir holds the zip of an artifact while it is read; os.TempDir when empty.
	TempDir string
	Now     func() time.Time
	// Wait blocks until t or ctx ends; tests pass a fake.
	Wait   func(ctx context.Context, t time.Time) error
	Logger *slog.Logger
}

// Service is the Collector: a queue worked within the Budget.
type Service struct {
	cfg Config

	mu     sync.Mutex
	queue  []*work
	queued map[string]bool
	wake   chan struct{}
}

var _ Collector = (*Service)(nil)

type workKind int

const (
	workLog workKind = iota
	workRun
)

type work struct {
	kind         workKind
	repositoryID int64
	runID        int64
	attempt      int
	jobID        int64
	seen         map[int64]bool // artifacts of a run already read in an earlier pass
}

func (w *work) key() string {
	if w.kind == workLog {
		return fmt.Sprintf("log/%d", w.jobID)
	}
	return fmt.Sprintf("run/%d/%d/%d", w.repositoryID, w.runID, w.attempt)
}

// New returns the collector.
func New(cfg Config) *Service {
	if cfg.Detectors == nil {
		cfg.Detectors = DefaultDetectors()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Wait == nil {
		cfg.Wait = func(ctx context.Context, t time.Time) error {
			tm := time.NewTimer(time.Until(t))
			defer tm.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-tm.C:
				return nil
			}
		}
	}
	return &Service{cfg: cfg, queued: map[string]bool{}, wake: make(chan struct{}, 1)}
}

// JobCompleted queues the log of one finished job.
func (s *Service) JobCompleted(_ context.Context, repositoryID, runID int64, runAttempt int, jobID int64) {
	s.enqueue(&work{kind: workLog, repositoryID: repositoryID, runID: runID, attempt: runAttempt, jobID: jobID})
}

// RunCompleted queues the artifacts of one finished run attempt.
func (s *Service) RunCompleted(_ context.Context, repositoryID, runID int64, runAttempt int) {
	s.enqueue(&work{kind: workRun, repositoryID: repositoryID, runID: runID, attempt: runAttempt, seen: map[int64]bool{}})
}

func (s *Service) enqueue(w *work) {
	s.mu.Lock()
	if !s.queued[w.key()] {
		s.queued[w.key()] = true
		s.queue = append(s.queue, w)
	}
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Pending is the number of queued items.
func (s *Service) Pending() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.queue)
}

// Run works the queue until ctx ends. A failed item is logged and dropped; it never stops the loop (R6.8).
func (s *Service) Run(ctx context.Context) error {
	for {
		handled, until, err := s.Step(ctx)
		if err != nil && ctx.Err() == nil {
			s.cfg.Logger.Warn("ci data collection failed", "err", err)
		}
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case handled:
		case !until.IsZero():
			if err := s.cfg.Wait(ctx, until); err != nil {
				return err
			}
		default:
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-s.wake:
			}
		}
	}
}

// Step works the first queued item. handled is false when the queue is empty or the budget is spent; in the
// second case until says when to try again and the item stays first in line.
func (s *Service) Step(ctx context.Context) (handled bool, until time.Time, err error) {
	s.mu.Lock()
	if len(s.queue) == 0 {
		s.mu.Unlock()
		return false, time.Time{}, nil
	}
	w := s.queue[0]
	s.mu.Unlock()

	lim, err := s.cfg.Limits(ctx)
	if err == nil && lim.Keep {
		if w.kind == workLog {
			until, err = s.fetchLog(ctx, w, lim)
		} else {
			until, err = s.fetchRun(ctx, w, lim)
		}
	}
	if !until.IsZero() {
		s.report()
		return false, until, err // the budget is spent: the item keeps its place
	}
	s.mu.Lock()
	if len(s.queue) > 0 && s.queue[0] == w {
		s.queue = s.queue[1:]
	}
	delete(s.queued, w.key())
	s.mu.Unlock()
	s.report()
	return true, time.Time{}, err
}

// report tells the budget how many jobs wait, for the screens (spec 4.10).
func (s *Service) report() {
	if b, ok := s.cfg.Budget.(interface{ SetBacklog(int) }); ok {
		b.SetBacklog(s.Pending())
	}
}

func (s *Service) stamp(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func (s *Service) expires(now time.Time, l Limits) string {
	if l.Days <= 0 {
		return ""
	}
	return s.stamp(now.Add(l.Days))
}

// fetchLog stores the log of one job (R5.2).
func (s *Service) fetchLog(ctx context.Context, w *work, lim Limits) (time.Time, error) {
	if row, ok, err := s.cfg.Index.CIJobLogOf(ctx, w.jobID); err != nil {
		return time.Time{}, err
	} else if ok && row.State != "failed" {
		return time.Time{}, nil // stored, too large, gone or expired: nothing to do again
	}
	repo, err := s.cfg.Repos.Repository(ctx, w.repositoryID)
	if err != nil {
		return time.Time{}, err
	}
	if ok, until := s.cfg.Budget.Take(ctx, repo.InstallationID, PriorityLogs, 1); !ok {
		return until, nil
	}
	now := s.cfg.Now()
	row := store.CIJobLog{JobID: w.jobID, RepositoryID: w.repositoryID, RunID: w.runID, RunAttempt: int64(w.attempt), FetchedAt: s.stamp(now)}
	rc, size, err := s.cfg.GitHub.JobLog(ctx, repo.InstallationID, repo.Owner, repo.Name, w.jobID)
	switch {
	case errors.Is(err, ErrGone):
		row.State, row.Reason = "missing", "GitHub no longer has the log"
		return time.Time{}, s.cfg.Index.SaveCIJobLog(ctx, row)
	case err != nil:
		return time.Time{}, fmt.Errorf("read the log of job %d: %w", w.jobID, err)
	}
	defer func() { _ = rc.Close() }()
	if size > MaxLogBytes {
		row.State, row.Reason = "too_large", "the log is over 64 MiB"
		return time.Time{}, s.cfg.Index.SaveCIJobLog(ctx, row)
	}
	st, err := s.cfg.Vault.PutLog(ctx, Key{RepositoryID: w.repositoryID, RunID: w.runID, RunAttempt: w.attempt, JobID: w.jobID}, rc)
	switch {
	case errors.Is(err, ErrTooLarge):
		row.State, row.Reason = "too_large", "the log is over 64 MiB"
	case errors.Is(err, ErrRedactor):
		row.State, row.Reason = "failed", "the redactor failed, nothing was stored"
	case err != nil && ctx.Err() != nil:
		return time.Time{}, ctx.Err()
	case err != nil:
		row.State, row.Reason = "failed", "the log could not be stored"
		s.cfg.Logger.Warn("store a job log", "job", w.jobID, "err", err)
	default:
		row.State, row.Path, row.Bytes, row.RawBytes, row.Lines, row.Sha256 = "stored", st.Path, st.Bytes, st.RawBytes, int64(st.Lines), st.SHA256
		row.ExpiresAt = s.expires(now, lim)
	}
	return time.Time{}, s.cfg.Index.SaveCIJobLog(ctx, row)
}

// fetchRun reads the report artifacts of one run attempt (R5.3).
func (s *Service) fetchRun(ctx context.Context, w *work, lim Limits) (time.Time, error) {
	repo, err := s.cfg.Repos.Repository(ctx, w.repositoryID)
	if err != nil {
		return time.Time{}, err
	}
	if ok, until := s.cfg.Budget.Take(ctx, repo.InstallationID, PriorityArtifacts, 1); !ok {
		return until, nil
	}
	arts, err := s.cfg.GitHub.RunArtifacts(ctx, repo.InstallationID, repo.Owner, repo.Name, w.runID)
	if err != nil {
		return time.Time{}, fmt.Errorf("list the artifacts of run %d: %w", w.runID, err)
	}
	sort.SliceStable(arts, func(i, j int) bool {
		gi, gj := s.guardName(arts[i].Name), s.guardName(arts[j].Name)
		if gi != gj {
			return gi
		}
		return arts[i].ID < arts[j].ID
	})
	var total int64
	var errs []error
	for _, a := range arts {
		if a.Expired || a.Size <= 0 || w.seen[a.ID] {
			continue
		}
		if a.Size > MaxReportBytes {
			s.skipArtifact(ctx, w, a, "too_large", "the artifact is over 50 MiB", lim)
			w.seen[a.ID] = true
			continue
		}
		if total+a.Size > MaxRunReportSum {
			s.skipArtifact(ctx, w, a, "too_large", "the run is over 200 MiB of artifacts", lim)
			w.seen[a.ID] = true
			continue
		}
		if ok, until := s.cfg.Budget.Take(ctx, repo.InstallationID, PriorityArtifacts, 1); !ok {
			return until, errors.Join(errs...)
		}
		total += a.Size
		if err := s.readArtifact(ctx, w, repo, a, lim); err != nil {
			if ctx.Err() != nil {
				return time.Time{}, ctx.Err()
			}
			errs = append(errs, fmt.Errorf("artifact %d of run %d: %w", a.ID, w.runID, err))
		}
		w.seen[a.ID] = true
	}
	return time.Time{}, errors.Join(errs...)
}

func (s *Service) guardName(name string) bool {
	if s.cfg.GuardrailArtifact == "" {
		return false
	}
	ok, _ := path.Match(s.cfg.GuardrailArtifact, name)
	return ok
}

// skipArtifact records an artifact that was not read, with the reason in words.
func (s *Service) skipArtifact(ctx context.Context, w *work, a Artifact, state, reason string, lim Limits) {
	now := s.cfg.Now()
	_, err := s.cfg.Index.SaveCIReportFile(ctx, store.CIReportFile{
		RepositoryID: w.repositoryID, RunID: w.runID, RunAttempt: int64(w.attempt), ArtifactID: a.ID, ArtifactName: a.Name,
		State: state, Reason: reason, FetchedAt: s.stamp(now), ExpiresAt: s.expires(now, lim),
	})
	if err != nil {
		s.cfg.Logger.Warn("record a skipped artifact", "artifact", a.ID, "err", err)
	}
}

const maxMembers = 500

var reportExt = map[string]bool{".xml": true, ".json": true, ".trx": true, ".jsonl": true, ".ndjson": true}

// readArtifact downloads one artifact zip and stores the members whose content is a report.
func (s *Service) readArtifact(ctx context.Context, w *work, repo Repo, a Artifact, lim Limits) error {
	rc, _, err := s.cfg.GitHub.DownloadArtifact(ctx, repo.InstallationID, repo.Owner, repo.Name, a.ID)
	if errors.Is(err, ErrGone) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()
	tmp, err := os.CreateTemp(s.cfg.TempDir, "ci-artifact-*")
	if err != nil {
		return err
	}
	defer func() { _ = tmp.Close(); _ = os.Remove(tmp.Name()) }()
	n, err := io.Copy(tmp, io.LimitReader(rc, MaxReportBytes+1))
	if err != nil {
		return err
	}
	if n > MaxReportBytes {
		s.skipArtifact(ctx, w, a, "too_large", "the artifact is over 50 MiB", lim)
		return nil
	}
	zr, err := zip.NewReader(tmp, n)
	if err != nil {
		s.skipArtifact(ctx, w, a, "broken", "the artifact is not a readable zip", lim)
		return nil
	}
	var errs []error
	for i, f := range zr.File {
		if i >= maxMembers {
			break
		}
		if f.FileInfo().IsDir() {
			continue
		}
		if err := s.readMember(ctx, w, a, f, lim); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *Service) readMember(ctx context.Context, w *work, a Artifact, f *zip.File, lim Limits) error {
	if _, ok, err := s.cfg.Index.CIReportFileOf(ctx, w.repositoryID, a.ID, f.Name); err != nil {
		return err
	} else if ok {
		return nil
	}
	now := s.cfg.Now()
	row := store.CIReportFile{
		RepositoryID: w.repositoryID, RunID: w.runID, RunAttempt: int64(w.attempt), ArtifactID: a.ID, ArtifactName: a.Name,
		Member: f.Name, FetchedAt: s.stamp(now), ExpiresAt: s.expires(now, lim),
	}
	if f.UncompressedSize64 > uint64(MaxReportBytes) {
		if reportExt[strings.ToLower(path.Ext(f.Name))] {
			row.State, row.Reason = "too_large", "the file is over 50 MiB"
			_, err := s.cfg.Index.SaveCIReportFile(ctx, row)
			return err
		}
		return nil
	}
	head, err := readHead(f)
	if err != nil {
		return nil // an unreadable member is not a report
	}
	format := Classify(head, s.cfg.Detectors)
	if format == "" {
		return nil
	}
	row.Format = format
	body, err := f.Open()
	if err != nil {
		return nil
	}
	defer func() { _ = body.Close() }()
	st, err := s.cfg.Vault.PutReport(ctx, Key{RepositoryID: w.repositoryID, RunID: w.runID, RunAttempt: w.attempt, ArtifactID: a.ID, Member: f.Name}, body)
	switch {
	case errors.Is(err, ErrTooLarge):
		row.State, row.Reason = "too_large", "the file is over 50 MiB"
	case errors.Is(err, ErrRedactor):
		row.State, row.Reason = "failed", "the redactor failed, nothing was stored"
	case err != nil && ctx.Err() != nil:
		return ctx.Err()
	case err != nil:
		row.State, row.Reason = "failed", "the file could not be stored"
		s.cfg.Logger.Warn("store a report file", "artifact", a.ID, "err", err)
	default:
		row.State, row.Path, row.Bytes = "stored", st.Path, st.Bytes
	}
	id, err := s.cfg.Index.SaveCIReportFile(ctx, row)
	if err != nil || row.State != "stored" || s.cfg.Sink == nil {
		return err
	}
	rec := ReportRecord{ID: id, RepositoryID: w.repositoryID, RunID: w.runID, RunAttempt: w.attempt, ArtifactID: a.ID, Member: f.Name, Format: format, Path: row.Path}
	if err := s.cfg.Sink.ReportStored(ctx, rec); err != nil {
		row.State, row.Reason = "broken", "the report could not be parsed"
		_, serr := s.cfg.Index.SaveCIReportFile(ctx, row)
		return errors.Join(fmt.Errorf("parse report %d: %w", id, err), serr)
	}
	row.State = "parsed"
	_, err = s.cfg.Index.SaveCIReportFile(ctx, row)
	return err
}

func readHead(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	buf := make([]byte, HeadBytes)
	n, err := io.ReadFull(rc, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return buf[:n], nil
}
