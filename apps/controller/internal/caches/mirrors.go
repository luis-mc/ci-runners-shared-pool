package caches

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/mirror"
)

// Mirrors is the cache holder's side of the Caches tab (caches R5.29, R5.71):
// the rows of the Actions receiver, the git mirrors, the tool cache and the
// registry mirror, and the wipe of one row. *mirror.Service implements it.
type Mirrors interface {
	Rows(ctx context.Context) ([]mirror.Row, error)
	Wipe(ctx context.Context, actor audit.Actor, admin, confirm bool, id string) (mirror.Plan, int64, error)
}

var _ Mirrors = (*mirror.Service)(nil)

// DefaultHolderID is the machine id the holder's rows carry when none is set.
const DefaultHolderID = "controller"

type mirrorWipe struct {
	jobs []string
	at   time.Time
}

type mirrorState struct {
	mu    sync.Mutex
	wipes map[string]*mirrorWipe // row id -> wipe
	done  map[string]time.Time
	last  map[string]string // row id -> why the last wipe failed
}

func (s *Service) holder() string {
	if s.o.HolderID != "" {
		return s.o.HolderID
	}
	return DefaultHolderID
}

func (s *Service) mirrorRows(ctx context.Context, machineID, kind string) ([]Cache, error) {
	if s.o.Mirrors == nil || (machineID != "" && machineID != s.holder()) {
		return nil, nil
	}
	rows, err := s.o.Mirrors.Rows(ctx)
	if err != nil {
		return nil, err
	}
	s.mirror.mu.Lock()
	defer s.mirror.mu.Unlock()
	var out []Cache
	for _, r := range rows {
		if kind != "" && r.Kind != kind {
			continue
		}
		c := Cache{ID: r.ID, Kind: r.Kind, MachineID: s.holder(), RunnerName: r.Name, Size: r.Size, Cap: r.Cap,
			Entries: r.Entries, State: StateReady, Name: r.Name, Status: r.Status, LastRefresh: r.LastRefresh,
			Hits: r.Hits, Misses: r.Misses, BytesServed: r.BytesServed, Error: r.Error}
		if w := s.mirror.wipes[r.ID]; w != nil {
			c.State, c.WaitingFor = StateWiping, append([]string{}, w.jobs...)
		}
		if e := s.mirror.last[r.ID]; e != "" && c.Error == "" {
			c.Error = e
		}
		if t, ok := s.mirror.done[r.ID]; ok {
			c.LastWipedAt = t
		}
		out = append(out, c)
	}
	return out, nil
}

// beginMirrorWipe is BeginWipe for a holder row. Without confirm it names what
// goes and the jobs it waits for (R5.31). With it, the wipe runs in the
// background: it waits for the jobs, checks again and deletes (R5.32).
func (s *Service) beginMirrorWipe(ctx context.Context, actor audit.Actor, id string, confirm bool) (Cache, error) {
	if s.o.Mirrors == nil {
		return Cache{}, ErrNotFound
	}
	if !confirm {
		p, _, err := s.o.Mirrors.Wipe(ctx, actor, true, false, id)
		switch {
		case errors.Is(err, mirror.ErrNeedsYes):
			items := make([]ConfirmItem, 0, len(p.Items)+len(p.Jobs))
			for _, it := range p.Items {
				items = append(items, ConfirmItem{Kind: "cache", Text: fmt.Sprintf("%s, %s", it.Text, FormatBytes(it.Size))})
			}
			if len(p.Items) == 0 {
				items = append(items, ConfirmItem{Kind: "cache", Text: "nothing is stored here"})
			}
			if len(p.Items) > 1 {
				items = append(items, ConfirmItem{Kind: "total", Text: "total " + FormatBytes(p.Total)})
			}
			for _, j := range p.Jobs {
				items = append(items, ConfirmItem{Kind: "job", Text: "waits for " + j})
			}
			return Cache{}, &NeedsConfirmation{Items: items}
		case errors.Is(err, mirror.ErrNoScope):
			return Cache{}, ErrNotFound
		case err != nil:
			return Cache{}, &RefusalError{Reason: err.Error()}
		}
		return Cache{}, ErrNotFound
	}
	s.mirror.mu.Lock()
	if s.mirror.wipes[id] != nil {
		s.mirror.mu.Unlock()
		return Cache{}, &RefusalError{Reason: "this cache is already being wiped"}
	}
	s.mirror.wipes[id] = &mirrorWipe{at: s.o.Now()}
	s.mirror.mu.Unlock()
	go func() {
		bg := audit.WithActor(context.WithoutCancel(ctx), actor)
		_, _, err := s.o.Mirrors.Wipe(bg, actor, true, true, id)
		s.mirror.mu.Lock()
		defer s.mirror.mu.Unlock()
		delete(s.mirror.wipes, id)
		if err == nil {
			s.mirror.done[id] = s.o.Now()
		}
		s.mirror.last[id] = errText(err)
	}()
	rows, _ := s.mirrorRows(ctx, "", "")
	for _, r := range rows {
		if r.ID == id {
			return r, nil
		}
	}
	return Cache{ID: id, MachineID: s.holder(), State: StateWiping}, nil
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return strings.TrimSpace(err.Error())
}
