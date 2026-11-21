package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/internal/jobsize"
	"github.com/luis-mc/ci-runners-shared-pool/internal/scheduler/slots"
)

// JobSizes is the database side of jobsize.Store (migration 00033, OPS67).
func (s *Store) JobSizes() *JobSizeStore { return &JobSizeStore{s} }

// JobSizeStore keeps the scan rows, the overrides and the measured sizes.
type JobSizeStore struct{ s *Store }

var _ jobsize.Store = (*JobSizeStore)(nil)

type sqlQuerier interface {
	QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error)
}

const scanCols = `repository, job, name, workflow, cpu_units, mem_units, cpu, gib, class, confidence, tests, prev_tests,
	est_sec, reasons, services, commit_sha, scanned_at, reset_note`

func readScanRows(ctx context.Context, q sqlQuerier, where string, args ...any) ([]jobsize.ScanRow, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+scanCols+` FROM job_size_scans `+where+` ORDER BY repository, job`, args...)
	if err != nil {
		return nil, fmt.Errorf("job size scans: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []jobsize.ScanRow
	for rows.Next() {
		var r jobsize.ScanRow
		var reasons, services, at, class, conf string
		if err := rows.Scan(&r.Repository, &r.Job, &r.Name, &r.Workflow, &r.Weight.CPU, &r.Weight.Memory, &r.CPU, &r.GiB,
			&class, &conf, &r.Tests, &r.PrevTests, &r.EstSec, &reasons, &services, &r.Commit, &at, &r.ResetNote); err != nil {
			return nil, err
		}
		r.Class, r.Confidence = jobsize.Class(class), jobsize.Confidence(conf)
		_ = json.Unmarshal([]byte(reasons), &r.Reasons)
		_ = json.Unmarshal([]byte(services), &r.Services)
		r.ScannedAt, _ = time.Parse(time.RFC3339Nano, at)
		out = append(out, r)
	}
	return out, rows.Err()
}

func readRules(ctx context.Context, q sqlQuerier, where string, args ...any) ([]jobsize.Rule, error) {
	rows, err := q.QueryContext(ctx, `SELECT repository, pattern, origin, class FROM job_size_rules `+where+
		` ORDER BY repository, origin, pattern`, args...)
	if err != nil {
		return nil, fmt.Errorf("job size rules: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []jobsize.Rule
	for rows.Next() {
		var r jobsize.Rule
		var class string
		if err := rows.Scan(&r.Repository, &r.Pattern, &r.Origin, &class); err != nil {
			return nil, err
		}
		r.Class = jobsize.Class(class)
		out = append(out, r)
	}
	return out, rows.Err()
}

func readMeasured(ctx context.Context, q sqlQuerier, where string, args ...any) ([]jobsize.Measured, error) {
	rows, err := q.QueryContext(ctx, `SELECT repository, job, cpu_units, mem_units, runs FROM job_size_measured `+where+` ORDER BY repository, job`, args...)
	if err != nil {
		return nil, fmt.Errorf("job size measured: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []jobsize.Measured
	for rows.Next() {
		var m jobsize.Measured
		if err := rows.Scan(&m.Repository, &m.Job, &m.Weight.CPU, &m.Weight.Memory, &m.Runs); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ScanRows lists the scan rows of a repository; "" lists every repository.
func (j *JobSizeStore) ScanRows(ctx context.Context, repository string) ([]jobsize.ScanRow, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	if repository == "" {
		return readScanRows(ctx, j.s.db, "")
	}
	return readScanRows(ctx, j.s.db, "WHERE repository = ?", repository)
}

// Rules lists the overrides of a repository.
func (j *JobSizeStore) Rules(ctx context.Context, repository string) ([]jobsize.Rule, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	return readRules(ctx, j.s.db, "WHERE repository = ?", repository)
}

// Measured lists the measured sizes of a repository.
func (j *JobSizeStore) Measured(ctx context.Context, repository string) ([]jobsize.Measured, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	return readMeasured(ctx, j.s.db, "WHERE repository = ?", repository)
}

// FileError says why .crsp.yml could not be read at the last scan; "" when it could.
func (j *JobSizeStore) FileError(ctx context.Context, repository string) (string, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	var e string
	err := j.s.db.QueryRowContext(ctx, `SELECT error FROM job_size_files WHERE repository = ?`, repository).Scan(&e)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return e, err
}

// Table gives the saved JSON of edited cost rows.
func (j *JobSizeStore) Table(ctx context.Context) (string, error) {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	var v string
	err := j.s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, jobsize.SettingKey).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

// SetConsoleRule saves or, with an empty class, removes a console override.
func (j *JobSizeStore) SetConsoleRule(ctx context.Context, repository, pattern string, class jobsize.Class) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	if class == "" {
		_, err := j.s.db.ExecContext(ctx, `DELETE FROM job_size_rules WHERE repository = ? AND pattern = ? AND origin = 'console'`, repository, pattern)
		return err
	}
	_, err := j.s.db.ExecContext(ctx, `INSERT INTO job_size_rules (repository, pattern, origin, class) VALUES (?, ?, 'console', ?)
		ON CONFLICT (repository, pattern, origin) DO UPDATE SET class = excluded.class`, repository, pattern, string(class))
	return err
}

// ReplaceScan swaps the scan rows and the file rules of a repository in one
// transaction and drops the measured size of each job in reset.
func (j *JobSizeStore) ReplaceScan(ctx context.Context, repository string, rows []jobsize.ScanRow, file []jobsize.Rule, fileErr string, reset []string) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	tx, err := j.s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, q := range []string{`DELETE FROM job_size_scans WHERE repository = ?`, `DELETE FROM job_size_rules WHERE repository = ? AND origin = 'file'`} {
		if _, err := tx.ExecContext(ctx, q, repository); err != nil {
			return err
		}
	}
	for _, r := range rows {
		reasons, _ := json.Marshal(strs(r.Reasons))
		services, _ := json.Marshal(strs(r.Services))
		if _, err := tx.ExecContext(ctx, `INSERT INTO job_size_scans (`+scanCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			repository, r.Job, r.Name, r.Workflow, r.Weight.CPU, r.Weight.Memory, r.CPU, r.GiB, string(r.Class), string(r.Confidence),
			r.Tests, r.PrevTests, r.EstSec, string(reasons), string(services), r.Commit, r.ScannedAt.UTC().Format(time.RFC3339Nano), r.ResetNote); err != nil {
			return err
		}
	}
	for _, f := range file {
		if _, err := tx.ExecContext(ctx, `INSERT INTO job_size_rules (repository, pattern, origin, class) VALUES (?, ?, 'file', ?)
			ON CONFLICT (repository, pattern, origin) DO UPDATE SET class = excluded.class`, repository, f.Pattern, string(f.Class)); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO job_size_files (repository, error) VALUES (?, ?)
		ON CONFLICT (repository) DO UPDATE SET error = excluded.error`, repository, fileErr); err != nil {
		return err
	}
	for _, job := range reset {
		if _, err := tx.ExecContext(ctx, `DELETE FROM job_size_measured WHERE repository = ? AND job = ?`, repository, job); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SetMeasured keeps a measured size; OPS66 calls it. Tests use it too.
func (j *JobSizeStore) SetMeasured(ctx context.Context, m jobsize.Measured) error {
	ctx, cancel := withDeadline(ctx)
	defer cancel()
	_, err := j.s.db.ExecContext(ctx, `INSERT INTO job_size_measured (repository, job, cpu_units, mem_units, runs) VALUES (?,?,?,?,?)
		ON CONFLICT (repository, job) DO UPDATE SET cpu_units = excluded.cpu_units, mem_units = excluded.mem_units, runs = excluded.runs`,
		m.Repository, m.Job, m.Weight.CPU, m.Weight.Memory, m.Runs)
	return err
}

func strs(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}

// loadSizes builds the precedence list the scheduler reads (R5.97): console
// rules, file rules, then one entry per scanned or measured job.
func loadSizes(ctx context.Context, q sqlQuerier) ([]slots.JobSize, error) {
	rules, err := readRules(ctx, q, "")
	if err != nil {
		return nil, err
	}
	scans, err := readScanRows(ctx, q, "")
	if err != nil {
		return nil, err
	}
	ms, err := readMeasured(ctx, q, "")
	if err != nil {
		return nil, err
	}
	var out []slots.JobSize
	for _, origin := range []string{jobsize.OriginConsole, jobsize.OriginFile} {
		for _, r := range rules {
			if r.Origin == origin {
				w := jobsize.ClassWeight(r.Class)
				out = append(out, slots.JobSize{Repository: r.Repository, Pattern: r.Pattern, Weight: slots.Weight{CPU: w.CPU, Memory: w.Memory},
					Class: string(r.Class), Source: string(jobsize.SourceOverride)})
			}
		}
	}
	mj := map[string]jobsize.Measured{}
	for _, m := range ms {
		mj[m.Repository+"\x00"+m.Job] = m
	}
	seen := map[string]bool{}
	for _, s := range scans {
		seen[s.Repository+"\x00"+s.Job] = true
		sz, _ := jobsize.Resolve(nil, optMeasured(mj, s.Repository, s.Job), &s, s.Job)
		out = append(out, slots.JobSize{Repository: s.Repository, Pattern: escapeGlob(s.Job), Weight: slots.Weight{CPU: sz.Weight.CPU, Memory: sz.Weight.Memory},
			Class: string(sz.Class), Source: string(sz.Source)})
	}
	keys := make([]string, 0, len(mj))
	for k := range mj {
		if !seen[k] {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		m := mj[k]
		sz, ok := jobsize.Resolve(nil, &m, nil, m.Job)
		if !ok {
			continue
		}
		out = append(out, slots.JobSize{Repository: m.Repository, Pattern: escapeGlob(m.Job), Weight: slots.Weight{CPU: sz.Weight.CPU, Memory: sz.Weight.Memory},
			Class: string(sz.Class), Source: string(sz.Source)})
	}
	return out, nil
}

func optMeasured(m map[string]jobsize.Measured, repo, job string) *jobsize.Measured {
	if v, ok := m[repo+"\x00"+job]; ok {
		return &v
	}
	return nil
}

// escapeGlob makes a job key a pattern that matches only itself.
func escapeGlob(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '*', '?', '[', '\\':
			out = append(out, '\\')
		}
		out = append(out, s[i])
	}
	return string(out)
}
