package toolcache

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
)

// State of a check, in the words the doctor uses.
type State string

const (
	StateOK   State = "ok"
	StateWarn State = "warn"
	StateFail State = "fail"
)

// Result is what the doctor shows for the tool cache of one machine.
type Result struct {
	State   State
	Message string // one plain sentence
	Root    string
	Bytes   int64
	Cap     int64
	Entries int
	// Leftovers counts stage and trash folders that should not be there.
	Leftovers int
}

// Checker is the interface the doctor takes (caches R5.64). The doctor package
// asks for it and shows the result; it owns no cache logic. The store is one.
type Checker interface {
	CheckToolCache(ctx context.Context) Result
}

var _ Checker = (*Store)(nil)

// nearFull is the share of the cap at which the check warns.
const nearFull = 0.9

// CheckToolCache measures the cache and says whether it is fit to share. It
// changes nothing.
func (s *Store) CheckToolCache(ctx context.Context) Result {
	r := Result{Root: s.Root, Cap: s.cap(), State: StateOK}
	if err := ctx.Err(); err != nil {
		return Result{Root: s.Root, State: StateWarn, Message: "the tool cache check was cancelled"}
	}
	if err := s.platform().Verify(s.fs(), s.Root); err != nil {
		r.State = StateFail
		if errors.Is(err, fs.ErrNotExist) {
			r.Message = "the shared tool cache folder " + s.Root + " does not exist"
		} else {
			r.Message = err.Error()
		}
		return r
	}
	if !s.writableProbe() {
		r.State = StateFail
		r.Message = "the shared tool cache folder " + s.Root + " cannot be written"
		return r
	}
	es, err := s.Entries()
	if err != nil {
		r.State = StateWarn
		r.Message = "the tool cache could not be measured: " + err.Error()
		return r
	}
	r.Entries = len(es)
	for _, e := range es {
		r.Bytes += e.Size
	}
	for _, d := range []string{trashDir, stageDir} {
		if des, err := s.fs().ReadDir(filepath.Join(s.Root, d)); err == nil {
			r.Leftovers += len(des)
		}
	}
	switch {
	case float64(r.Bytes) >= nearFull*float64(r.Cap):
		r.State = StateWarn
		r.Message = fmt.Sprintf("the tool cache holds %d of %d bytes; entries used in the last %v stay, so it may not shrink", r.Bytes, r.Cap, s.minAge())
	case r.Leftovers > 0:
		r.State = StateWarn
		r.Message = fmt.Sprintf("%d unfinished entries wait to be cleaned; the next sweep removes them", r.Leftovers)
	default:
		r.Message = fmt.Sprintf("the tool cache holds %d entries, %d of %d bytes", r.Entries, r.Bytes, r.Cap)
	}
	return r
}

// writableProbe makes and removes one folder, which is what a job does.
func (s *Store) writableProbe() bool {
	p := filepath.Join(s.Root, ".probe-"+s.token())
	if err := s.fs().Mkdir(p, 0o755); err != nil {
		return false
	}
	return s.fs().RemoveAll(p) == nil
}
