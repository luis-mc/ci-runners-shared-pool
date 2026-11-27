package collect

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
)

const (
	pruneBatch           = 1000
	maxFilesPerRepoSweep = 100000
)

// RetentionIndex is the part of the store the file retention needs. *store.Store implements it.
type RetentionIndex interface {
	ExpiredCIJobLogs(ctx context.Context, now string, limit int) ([]store.CIJobLog, error)
	ExpiredCIReportFiles(ctx context.Context, now string, limit int) ([]store.CIReportFile, error)
	CIJobLogsOldestFirst(ctx context.Context, repositoryID int64, limit int) ([]store.CIJobLog, error)
	CIReportFilesOldestFirst(ctx context.Context, repositoryID int64, limit int) ([]store.CIReportFile, error)
	CIDataBytesByRepository(ctx context.Context) ([]store.CIDataBytes, error)
	MarkCIJobLogExpired(ctx context.Context, jobID int64) error
	MarkCIReportFileExpired(ctx context.Context, id int64) error
}

// FileRetention is the Retention of R5.6: it deletes the stored CI files past their days, then the oldest
// files of a repository over its size cap, and marks their index rows expired. It never calls GitHub.
type FileRetention struct {
	Index RetentionIndex
	Vault Vault
}

var _ Retention = (*FileRetention)(nil)

type victim struct {
	at    string
	bytes int64
	path  string
	mark  func(context.Context) error
}

// Prune applies the limits once. It carries on after one file fails and returns all failures together.
func (r *FileRetention) Prune(ctx context.Context, now time.Time, l Limits) (int, int64, error) {
	var (
		removed int
		freed   int64
		errs    []error
	)
	drop := func(v victim) {
		if err := r.Vault.Remove(ctx, v.path); err != nil {
			errs = append(errs, fmt.Errorf("delete %s: %w", v.path, err))
			return
		}
		if err := v.mark(ctx); err != nil {
			errs = append(errs, err)
			return
		}
		removed++
		freed += v.bytes
	}
	stamp := now.UTC().Format(time.RFC3339)

	// 1. Rows that carry their own expiry time.
	for {
		logs, err := r.Index.ExpiredCIJobLogs(ctx, stamp, pruneBatch)
		if err != nil {
			errs = append(errs, err)
			break
		}
		for _, x := range logs {
			drop(logVictim(r.Index, x))
		}
		if len(logs) < pruneBatch {
			break
		}
	}
	for {
		files, err := r.Index.ExpiredCIReportFiles(ctx, stamp, pruneBatch)
		if err != nil {
			errs = append(errs, err)
			break
		}
		for _, x := range files {
			drop(reportVictim(r.Index, x))
		}
		if len(files) < pruneBatch {
			break
		}
	}

	// 2. Per repository: past the current days (the setting may have been lowered since the file was
	// stored), then the oldest first until under the size cap.
	sizes, err := r.Index.CIDataBytesByRepository(ctx)
	if err != nil {
		return removed, freed, errors.Join(append(errs, err)...)
	}
	cutoff := ""
	if l.Days > 0 {
		cutoff = now.Add(-l.Days).UTC().Format(time.RFC3339)
	}
	for _, sz := range sizes {
		logs, err := r.Index.CIJobLogsOldestFirst(ctx, sz.RepositoryID, maxFilesPerRepoSweep)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		files, err := r.Index.CIReportFilesOldestFirst(ctx, sz.RepositoryID, maxFilesPerRepoSweep)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		all := make([]victim, 0, len(logs)+len(files))
		var total int64
		for _, x := range logs {
			all = append(all, logVictim(r.Index, x))
			total += x.Bytes
		}
		for _, x := range files {
			all = append(all, reportVictim(r.Index, x))
			total += x.Bytes
		}
		sort.SliceStable(all, func(i, j int) bool { return all[i].at < all[j].at })
		for _, v := range all {
			tooOld := cutoff != "" && v.at < cutoff
			tooBig := l.BytesPerRepository > 0 && total > l.BytesPerRepository
			if !tooOld && !tooBig {
				break // the rest is newer and the repository fits
			}
			drop(v)
			total -= v.bytes
		}
	}
	return removed, freed, errors.Join(errs...)
}

func logVictim(ix RetentionIndex, x store.CIJobLog) victim {
	return victim{at: x.FetchedAt, bytes: x.Bytes, path: x.Path, mark: func(ctx context.Context) error { return ix.MarkCIJobLogExpired(ctx, x.JobID) }}
}

func reportVictim(ix RetentionIndex, x store.CIReportFile) victim {
	return victim{at: x.FetchedAt, bytes: x.Bytes, path: x.Path, mark: func(ctx context.Context) error { return ix.MarkCIReportFileExpired(ctx, x.ID) }}
}
