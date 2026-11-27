package ingest

import (
	"context"
	"errors"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata"
)

// maxGraphReads bounds the graph reads waiting or running; more are skipped and
// tried again by the next job event of the same commit.
const maxGraphReads = 64

// MaybeFetchGraph reads the needs graph of a workflow at a head commit in the
// background, at most once per commit and workflow for the life of the process,
// and only when the store does not hold it already. It never blocks the caller
// and never fails it: a read that fails stores Graph{OK:false}, so the screens
// fall back to the timing overlap. workflowPath may be "" for a job that came
// by webhook; the run's file is then looked up, with one call.
func (r *Recorder) MaybeFetchGraph(repositoryID int64, repository, workflowPath, workflowName string, runID int64, headSHA string) {
	if r.cfg.Graphs == nil || headSHA == "" || (workflowPath == "" && workflowName == "") {
		return
	}
	// A commit's workflow is known by its file and by its name: a job that came by
	// webhook names it before the file is looked up, a later job of the same commit
	// may carry the file. Both keys mark the read, so one commit is read once.
	var keys []graphKey
	if workflowPath != "" {
		keys = append(keys, graphKey{repositoryID, workflowPath, headSHA})
	}
	if workflowName != "" {
		keys = append(keys, graphKey{repositoryID, "name:" + workflowName, headSHA})
	}
	r.mu.Lock()
	for _, k := range keys {
		if _, seen := r.graphs.get(k); seen {
			r.mu.Unlock()
			return
		}
	}
	if r.pendingReads >= maxGraphReads {
		r.mu.Unlock()
		return
	}
	for _, k := range keys {
		r.graphs.put(k, struct{}{})
	}
	r.pendingReads++
	r.mu.Unlock()
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		defer func() {
			r.mu.Lock()
			r.pendingReads--
			r.mu.Unlock()
		}()
		r.sem <- struct{}{}
		defer func() { <-r.sem }()
		if err := r.fetchGraph(repositoryID, repository, workflowPath, workflowName, runID, headSHA); err != nil {
			r.mu.Lock()
			for _, k := range keys {
				r.graphs.del(k) // shut down before it finished; a later event may try again
			}
			r.mu.Unlock()
		}
	}()
}

// fetchGraph reads, parses and stores one graph. It returns an error only when
// the process is shutting down; every other failure is stored as OK:false.
func (r *Recorder) fetchGraph(repositoryID int64, repository, path, name string, runID int64, sha string) error {
	ctx, cancel := context.WithTimeout(r.bg, 2*time.Minute)
	defer cancel()
	g := jobsdata.Graph{RepositoryID: repositoryID, Workflow: path, HeadSHA: sha}
	fail := func(err error) error {
		if r.bg.Err() != nil {
			return r.bg.Err()
		}
		r.log.Debug("workflow graph not read", "repository", repository, "workflow", g.Workflow, "sha", sha, "err", err)
		g.OK, g.FetchedAt = false, r.cfg.Now()
		if g.Workflow == "" {
			g.Workflow = name
		}
		if err := r.cfg.Writer.PutGraph(ctx, g); err != nil {
			r.log.Warn("store a workflow graph", "repository", repository, "err", err)
		}
		return nil
	}
	if path == "" {
		p, err := r.cfg.Graphs.WorkflowPath(ctx, repository, runID)
		if err != nil || p == "" {
			if err == nil {
				err = errors.New("the run names no workflow file")
			}
			return fail(err)
		}
		path, g.Workflow = p, p
		r.mu.Lock()
		r.paths.put(pathKey{repository, name}, p)
		r.mu.Unlock()
	}
	if have, ok, err := r.cfg.Writer.GetGraph(ctx, repositoryID, path, sha); err == nil && ok && have.OK {
		return nil
	}
	content, err := r.cfg.Graphs.WorkflowFile(ctx, repository, path, sha)
	if err != nil {
		return fail(err)
	}
	wg, err := github.ParseWorkflowGraph(content)
	if err != nil {
		return fail(err)
	}
	g.Needs, g.Dynamic, g.OK, g.FetchedAt = wg.Needs, wg.Dynamic, true, r.cfg.Now()
	if err := r.cfg.Writer.PutGraph(ctx, g); err != nil {
		r.log.Warn("store a workflow graph", "repository", repository, "err", err)
	}
	return nil
}
