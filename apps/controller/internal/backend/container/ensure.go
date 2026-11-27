package container

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/sys"
	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
)

// Ensure image (registry R5.13 to R5.20): the order is the local engine, then
// the controller's answer to EnsureImage, then a pull by digest, else a local
// build and a push to staging in the background. A controller that does not
// answer is waited for; there is no local build then (owner decision D11).

// ErrNoController is what a Controller returns when nothing answered: no
// session, a dropped link or a timeout. The caller waits and asks again.
var ErrNoController = errors.New("the controller did not answer")

// Controller is the agent link as the backend sees it. The agent gives the
// real one (internal/agent/registry.go); tests give a fake.
type Controller interface {
	// Available says the controller told this agent it has the registry calls
	// (HelloAck protocol version 3 or more, R5.19). False keeps the old
	// behaviour: a local build, no push.
	Available() bool
	// EnsureImage and RegistryToken return an error when no answer came.
	EnsureImage(ctx context.Context, req *agentv1.EnsureImage) (*agentv1.EnsureImageAnswer, error)
	RegistryToken(ctx context.Context, req *agentv1.RegistryTokenRequest) (*agentv1.RegistryToken, error)
	// The reports are best effort and never block.
	PushReport(rep *agentv1.PushReport)
	PullReport(rep *agentv1.PullReport)
}

// Link is the controller of the agent process, set by the agent when it runs.
// A backend whose Config has no Controller uses it.
var Link = &ControllerSlot{}

// ControllerSlot forwards to the Controller set into it; empty it is not
// Available.
type ControllerSlot struct {
	mu sync.RWMutex
	c  Controller
}

// Set puts the controller into the slot.
func (s *ControllerSlot) Set(c Controller) { s.mu.Lock(); s.c = c; s.mu.Unlock() }

func (s *ControllerSlot) get() Controller { s.mu.RLock(); defer s.mu.RUnlock(); return s.c }

// Available implements Controller.
func (s *ControllerSlot) Available() bool { c := s.get(); return c != nil && c.Available() }

// EnsureImage implements Controller.
func (s *ControllerSlot) EnsureImage(ctx context.Context, r *agentv1.EnsureImage) (*agentv1.EnsureImageAnswer, error) {
	if c := s.get(); c != nil {
		return c.EnsureImage(ctx, r)
	}
	return nil, ErrNoController
}

// RegistryToken implements Controller.
func (s *ControllerSlot) RegistryToken(ctx context.Context, r *agentv1.RegistryTokenRequest) (*agentv1.RegistryToken, error) {
	if c := s.get(); c != nil {
		return c.RegistryToken(ctx, r)
	}
	return nil, ErrNoController
}

// PushReport implements Controller.
func (s *ControllerSlot) PushReport(r *agentv1.PushReport) {
	if c := s.get(); c != nil {
		c.PushReport(r)
	}
}

// PullReport implements Controller.
func (s *ControllerSlot) PullReport(r *agentv1.PullReport) {
	if c := s.get(); c != nil {
		c.PullReport(r)
	}
}

// RegistryRequest is what the registry needs to know about the image of a
// spec. The zero value is valid: the key is the backend's own key.
type RegistryRequest struct {
	Key          string // the image key; empty: KeyFor(spec)
	Arch         string // amd64 or arm64; empty: the spec's, else amd64
	KeyInputJSON string
	Repository   string
	JobID        string
	Commit       string
	Branch       string
}

// WaitingText is the job log line while the controller does not answer
// (registry R5.14); the time waited follows.
const WaitingText = "Waiting for the controller to say whether the image is in the registry"

// The other job log words of the registry flow. Each one is a row of the
// "Registry job-log words" table in docs/design-system/next-version/content.md;
// words_test.go fails when a string and its row differ.
const (
	waitedOpen    = " (waited "
	busyText      = "The registry is busy; asking again in "
	pullingText   = "Pulling the job image from the registry"
	pullRetryText = "The pull did not finish; trying again"
	pushFailText  = "The image stays on this machine; it was not pushed: "
	pushedText    = "Pushed the image to the registry staging"
	pullFailOpen  = "The pull from the registry failed ("
	pullFailClose = "); building the image here instead"
)

// waitingLine is the job log line of a wait: WaitingText and the time waited.
func waitingLine(d time.Duration) string { return WaitingText + waitedOpen + waited(d) + ")" }

// busyLine is the job log line when the registry says it is busy.
func busyLine(d time.Duration) string { return busyText + waited(d) }

const (
	pullTries       = 3
	pushTries       = 5
	reportEvery     = 2 * time.Second
	defaultBusyWait = 5 * time.Second
	tokenCallBound  = 2 * time.Minute
)

var backoffSteps = []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 15 * time.Second, 30 * time.Second, time.Minute}

func backoff(attempt int) time.Duration {
	if attempt >= len(backoffSteps) {
		attempt = len(backoffSteps) - 1
	}
	return backoffSteps[attempt]
}

func (b *Backend) now() time.Time {
	if b.cfg.Now != nil {
		return b.cfg.Now()
	}
	return time.Now()
}

// sleep waits d or until ctx ends.
func (b *Backend) sleep(ctx context.Context, d time.Duration) {
	if b.d.Sleep != nil {
		b.d.Sleep(ctx, d)
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

var idSeq atomic.Uint64

func nextID(prefix string) string { return prefix + "-" + strconv.FormatUint(idSeq.Add(1), 10) }

// controller returns the controller to ask, or nil when this backend builds
// as before (none set, or it did not say it has the registry).
func (b *Backend) controller() Controller {
	c := b.cfg.Controller
	if c == nil {
		c = Link
	}
	if !c.Available() {
		return nil
	}
	return c
}

func (b *Backend) registryRequest(spec backend.ImageSpec) RegistryRequest {
	var r RegistryRequest
	if b.cfg.RegistryRequest != nil {
		r = b.cfg.RegistryRequest(spec)
	}
	if r.Key == "" {
		r.Key = b.KeyFor(spec)
	}
	if r.Arch == "" {
		r.Arch = spec.Arch
	}
	if r.Arch == "" {
		r.Arch = "amd64"
	}
	return r
}

func waited(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%d s", int(d/time.Second))
	}
	return fmt.Sprintf("%d min %d s", int(d/time.Minute), int(d%time.Minute/time.Second))
}

// waitLoop calls fn until it answers. While nothing answers the job log says
// so with the time waited and the call is tried again with back-off; it ends
// only on an answer or when ctx ends.
func (b *Backend) waitLoop(ctx context.Context, fn func() bool) error {
	start := b.now()
	for attempt := 0; ; attempt++ {
		if fn() {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		b.log("prepare image", waitingLine(b.now().Sub(start)))
		b.sleep(ctx, backoff(attempt))
		if err := ctx.Err(); err != nil {
			return err
		}
	}
}

// ask puts EnsureImage until the controller gives a clear FOUND or NOT_FOUND.
// BUSY is asked again after retry_after_ms; no answer is waited for.
func (b *Backend) ask(ctx context.Context, ctl Controller, r RegistryRequest) (*agentv1.EnsureImageAnswer, error) {
	var got *agentv1.EnsureImageAnswer
	err := b.waitLoop(ctx, func() bool {
		for {
			ans, err := ctl.EnsureImage(ctx, &agentv1.EnsureImage{RequestId: nextID("ens"), Key: r.Key, Arch: r.Arch,
				KeyInputJson: r.KeyInputJSON, Repository: r.Repository, JobId: r.JobID})
			if err != nil || ans == nil || ctx.Err() != nil {
				return false
			}
			switch ans.GetOutcome() {
			case agentv1.EnsureImageAnswer_OUTCOME_FOUND, agentv1.EnsureImageAnswer_OUTCOME_NOT_FOUND:
				got = ans
				return true
			case agentv1.EnsureImageAnswer_OUTCOME_BUSY:
				d := time.Duration(ans.GetRetryAfterMs()) * time.Millisecond
				if d <= 0 {
					d = defaultBusyWait
				}
				b.log("prepare image", busyLine(d))
				b.sleep(ctx, d)
				if ctx.Err() != nil {
					return false
				}
			default:
				return false // an unclear answer is not "not in the registry"
			}
		}
	})
	return got, err
}

// token asks for a registry token, waiting for the controller. A refusal
// comes back as an error that is not ErrNoController.
func (b *Backend) token(ctx context.Context, ctl Controller, action agentv1.RegistryTokenRequest_Action, name string) (string, error) {
	var tok *agentv1.RegistryToken
	if err := b.waitLoop(ctx, func() bool {
		t, err := ctl.RegistryToken(ctx, &agentv1.RegistryTokenRequest{RequestId: nextID("tok"), Action: action, Names: []string{name}})
		if err != nil || t == nil {
			return false
		}
		tok = t
		return true
	}); err != nil {
		return "", err
	}
	if tok.GetRefused() != "" {
		return "", errors.New("the controller refused the registry token: " + tok.GetRefused())
	}
	if tok.GetToken() == "" {
		return "", errors.New("the controller gave an empty registry token")
	}
	if b.d.Secret != nil {
		b.d.Secret(tok.GetToken())
	}
	return tok.GetToken(), nil
}

// withDockerConfig writes a temporary docker config folder that holds the
// token for host, runs fn with its path, and deletes it. The token never goes
// in an argument, a log or a job (registry R5.10).
func (b *Backend) withDockerConfig(host, tok string, fn func(dir string) error) error {
	root := b.cfg.BuildRoot
	if root == "" {
		root = "."
	}
	dir := filepath.Join(root, "docker-config-"+nextID("cfg"))
	if err := b.d.FS.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	defer func() { _ = b.d.FS.RemoveAll(dir) }()
	cfg := `{"auths":{"` + host + `":{"registrytoken":"` + tok + `"}}}`
	if err := b.d.FS.WriteFile(filepath.Join(dir, "config.json"), []byte(cfg), 0o600); err != nil {
		return err
	}
	return fn(dir)
}

// runStream runs one engine call, giving output lines to onLine when the
// command runner can.
func (b *Backend) runStream(ctx context.Context, args []string, onLine func(string)) (sys.Result, error) {
	c, err := b.eng.Command(args...)
	if err != nil {
		return sys.Result{}, err
	}
	if sc, ok := b.d.Cmd.(sys.StreamCommands); ok && onLine != nil {
		return sc.RunStream(ctx, c, onLine)
	}
	return b.d.Cmd.Run(ctx, c)
}

var digestLine = regexp.MustCompile(`digest: (sha256:[0-9a-f]{64})`)

var digestShape = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func (b *Backend) pullReport(ctl Controller, id string, r RegistryRequest, digest string, src agentv1.PullReport_Source, st agentv1.PullReport_State, done, total int64, secs float64, errText string) {
	ctl.PullReport(&agentv1.PullReport{PullId: id, Key: r.Key, Arch: r.Arch, ManifestDigest: digest, Source: src, State: st,
		BytesDone: done, BytesTotal: total, Seconds: secs, Repository: r.Repository, JobId: r.JobID,
		Error: b.redact(errText), AtUnixMs: b.now().UnixMilli()})
}

// pull gets the image of an answer by digest, checks the digest and tags it
// crsp-pool:<key> (registry R5.13, R5.17, R5.18).
func (b *Backend) pull(ctx context.Context, ctl Controller, r RegistryRequest, ans *agentv1.EnsureImageAnswer, tag string) (err error) {
	digest, name, host := ans.GetManifestDigest(), ans.GetName(), ans.GetRegistryHost()
	if !digestShape.MatchString(digest) || name == "" || host == "" {
		return errors.New("the controller's answer has no usable name and digest")
	}
	ref := host + "/" + name + "@" + digest
	id := nextID("pull")
	start := b.now()
	total := ans.GetSizeBytes()
	b.pullReport(ctl, id, r, digest, agentv1.PullReport_SOURCE_PULLED, agentv1.PullReport_STATE_PULLING, 0, total, 0, "")
	b.log("prepare image", pullingText+sizeText(0, total, 0))
	defer func() {
		st, msg := agentv1.PullReport_STATE_DONE, ""
		if err != nil {
			st, msg = agentv1.PullReport_STATE_FAILED, err.Error()
		}
		d := total
		if err != nil {
			d = 0
		}
		b.pullReport(ctl, id, r, digest, agentv1.PullReport_SOURCE_PULLED, st, d, total, b.now().Sub(start).Seconds(), msg)
	}()
	for try := 0; try < pullTries; try++ {
		if try > 0 {
			b.log("prepare image", pullRetryText)
			b.sleep(ctx, backoff(try-1))
		}
		var tok string
		if tok, err = b.token(ctx, ctl, agentv1.RegistryTokenRequest_ACTION_PULL, name); err != nil {
			return err // a refusal is not retried; a wait ends only with ctx
		}
		prog := &pullProgress{total: total}
		last := b.now()
		b.ensureEngineCA(ctx, host)
		err = b.withDockerConfig(host, tok, func(dir string) error {
			res, rerr := b.runStream(ctx, []string{"--config", dir, "pull", ref}, func(line string) {
				prog.feed(line)
				if now := b.now(); now.Sub(last) >= reportEvery {
					last = now
					done, tot := prog.bytes()
					b.pullReport(ctl, id, r, digest, agentv1.PullReport_SOURCE_PULLED, agentv1.PullReport_STATE_PULLING, done, tot, now.Sub(start).Seconds(), "")
					b.log("prepare image", pullingText+sizeText(done, tot, now.Sub(start)))
				}
			})
			if rerr != nil {
				return rerr
			}
			if res.ExitCode != 0 {
				return fmt.Errorf("pull failed (exit %d): %s", res.ExitCode, b.redact(strings.TrimSpace(res.Stderr)))
			}
			return nil
		})
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err == nil {
			break
		}
	}
	if err != nil {
		return err
	}
	if verr := b.verifyDigest(ctx, ref, digest); verr != nil {
		_, _ = b.run(ctx, "rmi", ref)
		return verr
	}
	if err = b.step(ctx, "tag", []string{"tag", ref, tag}, ""); err != nil {
		return err
	}
	_, _ = b.run(ctx, "rmi", ref) // drops the by-digest name; the tag keeps the image
	return nil
}

func (b *Backend) verifyDigest(ctx context.Context, ref, want string) error {
	res, err := b.run(ctx, "image", "inspect", "--format", "{{range .RepoDigests}}{{.}} {{end}}", ref)
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("verify: %s", b.redact(strings.TrimSpace(res.Stderr)))
	}
	for _, d := range strings.Fields(res.Stdout) {
		if strings.HasSuffix(d, "@"+want) {
			return nil
		}
	}
	return fmt.Errorf("the pulled image does not have the digest %s", want)
}

// pushAsync pushes the local image to the staging name in the background; the
// job that needed the image does not wait for it (registry R5.16).
func (b *Backend) pushAsync(ctx context.Context, ctl Controller, r RegistryRequest, ans *agentv1.EnsureImageAnswer, tag string, build time.Duration) {
	b.pushWG.Add(1)
	go func() {
		defer b.pushWG.Done()
		b.push(context.WithoutCancel(ctx), ctl, r, ans, tag, build)
	}()
}

func (b *Backend) pushReport(ctl Controller, id string, r RegistryRequest, ans *agentv1.EnsureImageAnswer, digest string, st agentv1.PushReport_State, secs float64, errText string) {
	ctl.PushReport(&agentv1.PushReport{PushId: id, Key: r.Key, Arch: r.Arch, StagingName: ans.GetStagingName(), TargetName: ans.GetTargetName(),
		ManifestDigest: digest, State: st, Seconds: secs, Error: b.redact(errText), KeyInputJson: r.KeyInputJSON,
		Repository: r.Repository, CommitSha: r.Commit, Branch: r.Branch})
}

func (b *Backend) push(ctx context.Context, ctl Controller, r RegistryRequest, ans *agentv1.EnsureImageAnswer, tag string, _ time.Duration) {
	id := nextID("push")
	host, staging := ans.GetRegistryHost(), ans.GetStagingName()
	start := b.now()
	fail := func(msg string) {
		b.log("push image", pushFailText+msg)
		b.pushReport(ctl, id, r, ans, "", agentv1.PushReport_STATE_FAILED, b.now().Sub(start).Seconds(), msg)
	}
	if host == "" || staging == "" {
		fail("the controller gave no staging name")
		return
	}
	ref := host + "/" + staging + ":" + r.Key
	b.pushReport(ctl, id, r, ans, "", agentv1.PushReport_STATE_PUSHING, 0, "")
	var lastErr error
	for try := 0; try < pushTries; try++ {
		if try > 0 {
			b.sleep(ctx, backoff(try-1))
		}
		// While the controller is offline the push waits, and its state says so.
		var tok string
		var terr error
		if err := b.waitLoopQuiet(ctx, func() bool {
			ctx2, cancel := context.WithTimeout(ctx, tokenCallBound)
			defer cancel()
			t, err := ctl.RegistryToken(ctx2, &agentv1.RegistryTokenRequest{RequestId: nextID("tok"), Action: agentv1.RegistryTokenRequest_ACTION_PUSH, Names: []string{staging}})
			if err != nil || t == nil {
				b.pushReport(ctl, id, r, ans, "", agentv1.PushReport_STATE_WAITING_CONTROLLER, 0, "")
				return false
			}
			if t.GetRefused() != "" {
				terr = errors.New("the controller refused the registry token: " + t.GetRefused())
			} else {
				tok = t.GetToken()
			}
			return true
		}); err != nil {
			fail(err.Error())
			return
		}
		if terr != nil {
			fail(terr.Error())
			return
		}
		if b.d.Secret != nil {
			b.d.Secret(tok)
		}
		if lastErr = b.step(ctx, "tag", []string{"tag", tag, ref}, ""); lastErr != nil {
			continue
		}
		var digest string
		b.ensureEngineCA(ctx, host)
		lastErr = b.withDockerConfig(host, tok, func(dir string) error {
			res, err := b.runStream(ctx, []string{"--config", dir, "push", ref}, nil)
			if err != nil {
				return err
			}
			if res.ExitCode != 0 {
				return fmt.Errorf("push failed (exit %d): %s", res.ExitCode, b.redact(strings.TrimSpace(res.Stderr)))
			}
			m := digestLine.FindStringSubmatch(res.Stdout + "\n" + res.Stderr)
			if m == nil {
				return errors.New("the engine did not say the digest of the pushed image")
			}
			digest = m[1]
			return nil
		})
		if lastErr == nil {
			_, _ = b.run(ctx, "rmi", ref)
			b.log("push image", pushedText)
			b.pushReport(ctl, id, r, ans, digest, agentv1.PushReport_STATE_PUSHED, b.now().Sub(start).Seconds(), "")
			return
		}
	}
	fail(lastErr.Error())
}

// waitLoopQuiet is waitLoop without a job log line: a push has no job log.
func (b *Backend) waitLoopQuiet(ctx context.Context, fn func() bool) error {
	for attempt := 0; ; attempt++ {
		if fn() {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		b.sleep(ctx, backoff(attempt))
	}
}

// ---------------------------------------------------------------- pull progress

// pullProgress reads the per-layer lines of `docker pull`. With byte counts
// ("Downloading 12MB/45MB") it adds them up; without (a plain, non-terminal
// pull prints none) it scales the size the controller gave by finished layers.
type pullProgress struct {
	mu     sync.Mutex
	total  int64
	layers map[string]*layerState
}

type layerState struct {
	cur, tot int64
	done     bool
}

var (
	layerLine = regexp.MustCompile(`^([0-9a-f]{6,64}):\s*(.*)$`)
	byteCount = regexp.MustCompile(`([0-9.]+)\s*([kKMGT]?i?B)\s*/\s*([0-9.]+)\s*([kKMGT]?i?B)`)
)

func (p *pullProgress) feed(line string) {
	m := layerLine.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.layers == nil {
		p.layers = map[string]*layerState{}
	}
	l := p.layers[m[1]]
	if l == nil {
		l = &layerState{}
		p.layers[m[1]] = l
	}
	st := strings.ToLower(m[2])
	switch {
	case strings.HasPrefix(st, "pull complete"), strings.HasPrefix(st, "already exists"), strings.HasPrefix(st, "download complete"):
		l.done = true
		if l.tot > 0 {
			l.cur = l.tot
		}
	case strings.HasPrefix(st, "downloading"), strings.HasPrefix(st, "extracting"):
		if bm := byteCount.FindStringSubmatch(m[2]); bm != nil && strings.HasPrefix(st, "downloading") {
			l.cur, l.tot = toBytes(bm[1], bm[2]), toBytes(bm[3], bm[4])
		}
	}
}

func toBytes(num, unit string) int64 {
	f, _ := strconv.ParseFloat(num, 64)
	mult := map[string]float64{"B": 1, "KB": 1e3, "KIB": 1 << 10, "MB": 1e6, "MIB": 1 << 20, "GB": 1e9, "GIB": 1 << 30, "TB": 1e12}[strings.ToUpper(unit)]
	if mult == 0 {
		mult = 1
	}
	return int64(f * mult)
}

// bytes returns bytes done and the total.
func (p *pullProgress) bytes() (done, total int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var sumCur, sumTot int64
	haveBytes, finished := false, 0
	for _, l := range p.layers {
		if l.done {
			finished++
		}
		if l.tot > 0 {
			haveBytes = true
			sumCur += l.cur
			sumTot += l.tot
		}
	}
	if haveBytes && (p.total == 0 || sumTot >= p.total) {
		return sumCur, sumTot
	}
	if p.total > 0 && len(p.layers) > 0 {
		return p.total * int64(finished) / int64(len(p.layers)), p.total
	}
	return 0, p.total
}

func gb(n int64) string { return strconv.FormatFloat(float64(n)/1e9, 'f', 1, 64) }

// sizeText is the tail of the progress line, ", 2.1 of 3.4 GB, about 1 min
// left", or "" when the size is not known (registry R5.17).
func sizeText(done, total int64, elapsed time.Duration) string {
	if total <= 0 {
		return ""
	}
	s := ", " + gb(done) + " of " + gb(total) + " GB"
	if done > 0 && done < total && elapsed > 0 {
		left := time.Duration(float64(elapsed) * float64(total-done) / float64(done))
		if left < time.Minute {
			s += ", less than a minute left"
		} else {
			s += ", about " + strconv.Itoa(int((left+30*time.Second)/time.Minute)) + " min left"
		}
	}
	return s
}
