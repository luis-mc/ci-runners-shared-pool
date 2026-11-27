// Package runnerswap replaces the files of one persistent runner on this
// machine: check the bundle, unpack it beside the runner, stop the service,
// swap by renames, start the service, and put the previous files back when any
// step fails (runners R5.38, R6.4).
//
// The caller drains first and checks that the runner is idle, in one step,
// before it calls Swap; Swap never looks at jobs. It touches only the entries
// the bundle holds, so the registration, the credentials, the .env, the hooks
// and the work folder stay where they are.
package runnerswap

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Folder names Swap makes inside the runner folder. Previous holds the files of
// the version before the last swap, for rollback and for a later manual undo.
const (
	StageDir    = ".crsp-stage"
	PreviousDir = ".crsp-prev"
	metaFile    = ".crsp.json"
	prevVersion = ".version"
)

// Errors a caller can tell apart.
var (
	// ErrChecksum is a bundle whose SHA-256 is not the one sent. Nothing was touched.
	ErrChecksum = errors.New("the runner bundle does not match its SHA-256; nothing was changed")
	// ErrNotManaged is a folder CRSP did not make (no meta file). Nothing was touched.
	ErrNotManaged = errors.New("this folder is not a runner CRSP made; nothing was changed")
)

// Host starts and stops the runner's service on this OS.
type Host interface {
	Stop(ctx context.Context, service string) error
	Start(ctx context.Context, service string) error
}

// Request is one swap.
type Request struct {
	// Folder is the runner folder.
	Folder  string
	Service string
	Version string
	SHA256  string
	Bundle  []byte
}

// Result says what happened.
type Result struct {
	// Current is true when the folder already runs Version; nothing was changed.
	Current bool
	// From is the version that was in the folder.
	From string
	// RolledBack is true when a step failed and the previous files were put back.
	RolledBack bool
}

// Swapper does the swap. The zero value but Host works with the real file system.
type Swapper struct {
	Host Host
	// Unpack and Rename are seams for tests; nil uses Unpack and os.Rename.
	Unpack func(data []byte, dest string) error
	Rename func(oldpath, newpath string) error
}

func (s *Swapper) unpack(data []byte, dest string) error {
	if s.Unpack != nil {
		return s.Unpack(data, dest)
	}
	return Unpack(data, dest)
}

func (s *Swapper) rename(a, b string) error {
	if s.Rename != nil {
		return s.Rename(a, b)
	}
	return os.Rename(a, b)
}

// Verify checks the bundle against its SHA-256.
func Verify(bundle []byte, want string) error {
	sum := sha256.Sum256(bundle)
	if len(bundle) == 0 || !strings.EqualFold(hex.EncodeToString(sum[:]), strings.TrimSpace(want)) {
		return ErrChecksum
	}
	return nil
}

// Swap replaces the runner files. On an error the result says whether the
// previous files were put back; an error with RolledBack false means the
// folder was never changed.
func (s *Swapper) Swap(ctx context.Context, req Request) (Result, error) {
	var res Result
	if err := Verify(req.Bundle, req.SHA256); err != nil {
		return res, err
	}
	meta, err := readMeta(req.Folder)
	if err != nil {
		return res, err
	}
	res.From, _ = meta["runner_version"].(string)
	if res.From == req.Version {
		res.Current = true
		return res, nil
	}
	stage := filepath.Join(req.Folder, StageDir)
	prev := filepath.Join(req.Folder, PreviousDir)
	if err := os.RemoveAll(stage); err != nil {
		return res, err
	}
	if err := s.unpack(req.Bundle, stage); err != nil {
		return res, errors.Join(fmt.Errorf("unpacking the runner bundle: %w", err), os.RemoveAll(stage))
	}
	entries, err := os.ReadDir(stage)
	if err != nil || len(entries) == 0 {
		return res, errors.Join(errors.New("the runner bundle is empty; nothing was changed"), err, os.RemoveAll(stage))
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}

	if err := s.Host.Stop(ctx, req.Service); err != nil {
		// The service may be half stopped: start it again, change nothing.
		return res, errors.Join(fmt.Errorf("stopping the runner service: %w", err), s.Host.Start(ctx, req.Service), os.RemoveAll(stage))
	}
	if err := os.RemoveAll(prev); err != nil {
		return res, errors.Join(err, s.Host.Start(ctx, req.Service))
	}
	if err := os.MkdirAll(prev, 0o750); err != nil {
		return res, errors.Join(err, s.Host.Start(ctx, req.Service))
	}

	moved, placed, err := s.swapEntries(req.Folder, stage, prev, names)
	if err == nil {
		err = writeMeta(req.Folder, meta, req.Version)
	}
	if err == nil {
		err = os.WriteFile(filepath.Join(prev, prevVersion), []byte(res.From), 0o600)
	}
	if err == nil {
		if err = s.Host.Start(ctx, req.Service); err != nil {
			err = fmt.Errorf("starting the runner service on the new files: %w", err)
		}
	}
	if err != nil {
		return s.rollback(ctx, req, res, meta, moved, placed, err)
	}
	_ = os.RemoveAll(stage) // best effort: a leftover stage is removed by the next swap
	return res, nil
}

// swapEntries moves each live entry into prev and each staged entry into the
// folder. It returns the names it moved and placed, also when it stops early.
func (s *Swapper) swapEntries(folder, stage, prev string, names []string) (moved, placed []string, err error) {
	for _, n := range names {
		live := filepath.Join(folder, n)
		if _, statErr := os.Lstat(live); statErr == nil {
			if err = s.rename(live, filepath.Join(prev, n)); err != nil {
				return moved, placed, fmt.Errorf("moving %s aside: %w", n, err)
			}
			moved = append(moved, n)
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return moved, placed, statErr
		}
		if err = s.rename(filepath.Join(stage, n), live); err != nil {
			return moved, placed, fmt.Errorf("putting %s in place: %w", n, err)
		}
		placed = append(placed, n)
	}
	return moved, placed, nil
}

// rollback puts the previous files back, restores the meta file and starts the
// service on them. The returned error always carries the cause.
func (s *Swapper) rollback(ctx context.Context, req Request, res Result, meta map[string]any, moved, placed []string, cause error) (Result, error) {
	// Rollback must finish even when the request context ended.
	ctx = context.WithoutCancel(ctx)
	prev := filepath.Join(req.Folder, PreviousDir)
	var errs []error
	if err := s.Host.Stop(ctx, req.Service); err != nil {
		errs = append(errs, fmt.Errorf("stopping the service for the rollback: %w", err))
	}
	for _, n := range placed {
		if err := os.RemoveAll(filepath.Join(req.Folder, n)); err != nil {
			errs = append(errs, err)
		}
	}
	for _, n := range moved {
		if err := os.Rename(filepath.Join(prev, n), filepath.Join(req.Folder, n)); err != nil {
			errs = append(errs, fmt.Errorf("putting back %s: %w", n, err))
		}
	}
	if err := writeMeta(req.Folder, meta, res.From); err != nil {
		errs = append(errs, err)
	}
	if err := s.Host.Start(ctx, req.Service); err != nil {
		errs = append(errs, fmt.Errorf("starting the service on the previous files: %w", err))
	}
	_ = os.RemoveAll(filepath.Join(req.Folder, StageDir))
	res.RolledBack = len(errs) == 0
	if len(errs) > 0 {
		return res, fmt.Errorf("%w; the rollback also failed, the runner needs a look: %w", cause, errors.Join(errs...))
	}
	return res, fmt.Errorf("%w; the previous version %s was put back", cause, res.From)
}

func readMeta(folder string) (map[string]any, error) {
	b, err := os.ReadFile(filepath.Join(folder, metaFile)) //nolint:gosec // the folder is the runner's own
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotManaged
		}
		return nil, err
	}
	m := map[string]any{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotManaged, err)
	}
	return m, nil
}

func writeMeta(folder string, meta map[string]any, version string) error {
	out := make(map[string]any, len(meta))
	for k, v := range meta {
		out[k] = v
	}
	out["runner_version"] = version
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(folder, metaFile), b, 0o600)
}
