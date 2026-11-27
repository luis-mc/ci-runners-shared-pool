package repos

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
)

// poolLabelSets is the label sets the pool's runners can carry, one per served
// OS (R5.30).
func (s *Service) poolLabelSets(ctx context.Context) ([][]string, error) {
	capy, err := s.osCapacity(ctx)
	if err != nil {
		return nil, err
	}
	var sets [][]string
	for _, v := range variableOrder {
		if labels, ok := capy[v.OS]; ok {
			sets = append(sets, labelList(v.OS, labels, nil))
		}
	}
	return sets, nil
}

// workflowFiles reads the workflow files with ETags kept in memory, so an
// unchanged file costs no download (R5.30, R6.6).
func (s *Service) workflowFiles(ctx context.Context, r Repository) (map[string][]byte, error) {
	s.mu.Lock()
	etags := map[string]string{}
	for k, v := range s.etags[r.ID] {
		etags[k] = v
	}
	cached := s.cache[r.ID]
	s.mu.Unlock()

	files, err := s.cfg.GitHub.ListWorkflowFiles(github.Background(ctx), r.InstallationID, r.Owner, r.Name, etags)
	if err != nil {
		return nil, err
	}
	out := map[string][]byte{}
	newEtags := map[string]string{}
	newCache := map[string][]byte{}
	for _, f := range files {
		content := f.Content
		if content == nil {
			content = cached[f.Path]
		}
		out[f.Path] = content
		newEtags[f.Path] = f.ETag
		newCache[f.Path] = content
	}
	s.mu.Lock()
	s.etags[r.ID], s.cache[r.ID] = newEtags, newCache
	s.mu.Unlock()
	return out, nil
}

// CheckAllRunsOn runs CheckRunsOn for every enabled repository (R5.30, every
// hour). On a rate limit it stops and returns the error, which carries the
// reset time (R6.6); any other error is kept and the next repository goes on.
func (s *Service) CheckAllRunsOn(ctx context.Context) error {
	on := true
	rs, err := s.cfg.Store.ListRepositories(ctx, ListFilter{Pool: &on})
	if err != nil {
		return err
	}
	var errs []error
	for _, r := range rs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.CheckRunsOn(ctx, r.ID); err != nil {
			if errors.Is(err, github.ErrRateLimited) {
				return err
			}
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// CheckRunsOn reads every workflow file of a repository and stores the
// findings with file and line (R5.30). It runs on enable, on policy change and
// every hour.
func (s *Service) CheckRunsOn(ctx context.Context, repoID int64) error {
	r, err := s.cfg.Store.GetRepository(ctx, repoID)
	if err != nil {
		return err
	}
	if !r.PoolOn {
		return nil
	}
	files, err := s.workflowFiles(ctx, r)
	if err != nil {
		return err
	}
	vars, err := s.cfg.GitHub.ListVariables(github.Background(ctx), r.InstallationID, r.Owner, r.Name)
	if err != nil {
		return err
	}
	vm := map[string]string{}
	for _, v := range vars {
		vm[v.Name] = v.Value
	}
	pool, err := s.poolLabelSets(ctx)
	if err != nil {
		return err
	}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	var all []Finding
	for _, p := range paths {
		for _, f := range Check(p, ParseRunsOn(files[p]), vm, pool) {
			f.RepositoryID = repoID
			all = append(all, f)
		}
	}
	return s.cfg.Store.ReplaceFindings(ctx, repoID, all, s.cfg.Now())
}

// FindingsAt returns the stored runs-on findings and when they were checked.
func (s *Service) FindingsAt(ctx context.Context, repoID int64) ([]Finding, time.Time, error) {
	return s.cfg.Store.ListFindings(ctx, repoID)
}

// Findings returns the stored runs-on findings.
func (s *Service) Findings(ctx context.Context, repoID int64) ([]Finding, error) {
	fs, _, err := s.cfg.Store.ListFindings(ctx, repoID)
	return fs, err
}
