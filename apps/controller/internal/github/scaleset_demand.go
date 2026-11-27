package github

import (
	"context"
	"errors"
	"fmt"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/addresses"
	"strings"
	"sync"
	"time"
)

// ScaleSetTarget is one scope that gets a scale-set session.
type ScaleSetTarget struct {
	InstallationID int64
	Owner, Repo    string
}

func (t ScaleSetTarget) key() string { return strings.ToLower(t.Owner + "/" + t.Repo) }

// RegistrationSource gives a registration token of a scope (R5.31). The REST
// client is one.
type RegistrationSource interface {
	CreateRegistrationToken(ctx context.Context, installationID int64, scope RunnerScope) (string, error)
}

// ScaleSetDemand is the scale-set Demand source (R5.35, R5.40): one session
// per target, each turning offered, started and finished jobs into Job
// changes. It fails fast: when a session cannot be opened or drops, the
// channel closes and Err says why, so a Failover can fall back to polling.
type ScaleSetDemand struct {
	Targets func(ctx context.Context) ([]ScaleSetTarget, error)
	Tokens  RegistrationSource
	// Name is the scale set name (the pool name), so the labels of a job match.
	Name    string
	APIBase string
	WebBase string
	// New builds the client of one target; tests replace it. The default uses
	// APIBase, WebBase and Tokens.
	New func(t ScaleSetTarget) *ScaleSetClient
	// Refresh is how often the target list is read again (default 1 minute).
	Refresh time.Duration
	Now     func() time.Time

	mu         sync.Mutex
	clients    map[string]*ScaleSetClient
	err        error
	lastDemand map[string]time.Time // lower-case owner/repo -> last queued job
}

// LastDemand is the time a queued job of the repository last came through a
// session (what the quiet-demand watchdog reads, R5.37).
func (d *ScaleSetDemand) LastDemand(fullName string) time.Time {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.lastDemand[strings.ToLower(fullName)]
}

var _ Demand = (*ScaleSetDemand)(nil)

// Err is why the last Jobs stream ended, or nil.
func (d *ScaleSetDemand) Err() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.err
}

func (d *ScaleSetDemand) fail(err error) {
	d.mu.Lock()
	if d.err == nil {
		d.err = err
	}
	d.mu.Unlock()
}

func (d *ScaleSetDemand) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

func (d *ScaleSetDemand) client(t ScaleSetTarget) *ScaleSetClient {
	if d.New != nil {
		return d.New(t)
	}
	web := d.WebBase
	if web == "" {
		web = addresses.DefaultGitHubURL
	}
	return &ScaleSetClient{
		APIBase: d.APIBase, ConfigURL: strings.TrimRight(web, "/") + "/" + t.Owner + "/" + t.Repo, Name: d.Name,
		Labels: []string{d.Name},
		RegistrationToken: func(ctx context.Context) (string, error) {
			return d.Tokens.CreateRegistrationToken(ctx, t.InstallationID, RunnerScope{Owner: t.Owner, Repo: t.Repo})
		},
	}
}

// JIT gives the JIT config for a runner of a job that came from a scale set.
// The caller passes it on and drops it.
func (d *ScaleSetDemand) JIT(ctx context.Context, j Job, name string) (JITConfig, error) {
	d.mu.Lock()
	c := d.clients[strings.ToLower(j.Repository)]
	d.mu.Unlock()
	if c == nil || c.ScaleSetID() != j.ScaleSetID {
		return JITConfig{}, errors.New("github scale set: no session for this job's repository")
	}
	return c.GenerateJITConfig(ctx, name)
}

// Jobs implements Demand.
func (d *ScaleSetDemand) Jobs(ctx context.Context) (<-chan Job, error) {
	if d.Targets == nil || (d.New == nil && d.Tokens == nil) || d.Name == "" {
		return nil, errors.New("github scale set: targets, a token source and a name are needed")
	}
	d.mu.Lock()
	d.err, d.clients = nil, map[string]*ScaleSetClient{}
	d.mu.Unlock()
	out := make(chan Job)
	ctx, cancel := context.WithCancel(ctx)
	refresh := d.Refresh
	if refresh <= 0 {
		refresh = time.Minute
	}
	go func() {
		var wg sync.WaitGroup
		defer func() {
			cancel()
			wg.Wait()
			close(out)
		}()
		running := map[string]context.CancelFunc{}
		failed := make(chan error, 1)
		tick := time.NewTicker(refresh)
		defer tick.Stop()
		for {
			ts, err := d.Targets(ctx)
			if err != nil {
				d.fail(fmt.Errorf("list scale-set targets: %w", err))
				return
			}
			want := map[string]ScaleSetTarget{}
			for _, t := range ts {
				want[t.key()] = t
			}
			for k, stop := range running {
				if _, ok := want[k]; !ok {
					stop()
					delete(running, k)
				}
			}
			for k, t := range want {
				if _, ok := running[k]; ok {
					continue
				}
				tctx, stop := context.WithCancel(ctx)
				running[k] = stop
				wg.Add(1)
				go func() {
					defer wg.Done()
					if err := d.session(tctx, t, out); err != nil && tctx.Err() == nil {
						select {
						case failed <- err:
						default:
						}
					}
				}()
			}
			select {
			case <-ctx.Done():
				return
			case err := <-failed:
				d.fail(err)
				return
			case <-tick.C:
			}
		}
	}()
	return out, nil
}

// session runs one target's session until ctx ends or it fails.
func (d *ScaleSetDemand) session(ctx context.Context, t ScaleSetTarget, out chan<- Job) error {
	c := d.client(t)
	if err := c.Connect(ctx); err != nil {
		return err
	}
	s, err := c.OpenSession(ctx)
	if err != nil {
		return err
	}
	d.mu.Lock()
	d.clients[t.key()] = c
	d.mu.Unlock()
	defer func() {
		// The caller's ctx may be done; the session is closed with its own.
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = s.Close(cctx)
	}()
	for {
		m, err := s.Next(ctx)
		if err != nil {
			return err
		}
		if m == nil {
			continue
		}
		jobs, err := d.handle(ctx, s, t, c.ScaleSetID(), m)
		if err != nil {
			return err
		}
		for _, j := range jobs {
			if j.Status == "queued" {
				d.mu.Lock()
				if d.lastDemand == nil {
					d.lastDemand = map[string]time.Time{}
				}
				d.lastDemand[strings.ToLower(j.Repository)] = d.now()
				d.mu.Unlock()
			}
			select {
			case out <- j:
			case <-ctx.Done():
				return nil
			}
		}
		if err := s.Ack(ctx, m.ID); err != nil {
			return err
		}
	}
}

// handle turns one message into job changes. Offered jobs are acquired first;
// only the ones GitHub gave to this session are reported as queued.
func (d *ScaleSetDemand) handle(ctx context.Context, s *ScaleSetSession, t ScaleSetTarget, setID int64, m *ScaleSetMessage) ([]Job, error) {
	var offered []int64
	byID := map[int64]ScaleSetJob{}
	for _, j := range m.Jobs {
		if j.Type == "JobAvailable" {
			offered = append(offered, j.RequestID)
			byID[j.RequestID] = j
		}
	}
	var out []Job
	if len(offered) > 0 {
		got, err := s.Acquire(ctx, offered)
		if err != nil {
			return nil, err
		}
		for _, id := range got {
			if j, ok := byID[id]; ok {
				out = append(out, d.job(t, setID, j, "queued"))
			}
		}
	}
	for _, j := range m.Jobs {
		switch j.Type {
		case "JobStarted":
			out = append(out, d.job(t, setID, j, "in_progress"))
		case "JobCompleted":
			out = append(out, d.job(t, setID, j, "completed"))
		}
	}
	return out, nil
}

func (d *ScaleSetDemand) job(t ScaleSetTarget, setID int64, j ScaleSetJob, status string) Job {
	repo := t.Owner + "/" + t.Repo
	if j.Owner != "" && j.Repository != "" {
		repo = j.Owner + "/" + j.Repository
	}
	labels := append([]string{"self-hosted", "linux"}, j.Labels...)
	out := Job{ID: j.RequestID, Repository: repo, Event: j.Event, Labels: labels, Status: status, RunnerName: j.RunnerName,
		CreatedAt: d.now(), ScaleSetID: setID,
		// The message does not say whether a pull request comes from a fork.
		// Fail closed: every pull request event counts as a fork until the
		// repository's policy says otherwise.
		FromFork: strings.HasPrefix(j.Event, "pull_request")}
	if status == "completed" {
		out.Conclusion = j.Result
		out.CompletedAt = d.now()
	}
	return out
}
