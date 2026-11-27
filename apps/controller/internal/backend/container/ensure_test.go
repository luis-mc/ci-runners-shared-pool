package container

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
)

// fakeCtl is the controller side of the agent link, scripted per test.
type fakeCtl struct {
	mu       sync.Mutex
	off      bool // not available: an old controller
	ensure   func(n int) (*agentv1.EnsureImageAnswer, error)
	token    func(n int, req *agentv1.RegistryTokenRequest) (*agentv1.RegistryToken, error)
	nEnsure  int
	nToken   int
	tokenReq []*agentv1.RegistryTokenRequest
	ensured  []*agentv1.EnsureImage
	pulls    []*agentv1.PullReport
	pushes   []*agentv1.PushReport
}

func (c *fakeCtl) Available() bool { return !c.off }
func (c *fakeCtl) EnsureImage(_ context.Context, r *agentv1.EnsureImage) (*agentv1.EnsureImageAnswer, error) {
	c.mu.Lock()
	c.nEnsure++
	n := c.nEnsure
	c.ensured = append(c.ensured, r)
	c.mu.Unlock()
	return c.ensure(n)
}
func (c *fakeCtl) RegistryToken(_ context.Context, r *agentv1.RegistryTokenRequest) (*agentv1.RegistryToken, error) {
	c.mu.Lock()
	c.nToken++
	n := c.nToken
	c.tokenReq = append(c.tokenReq, r)
	c.mu.Unlock()
	if c.token == nil {
		return &agentv1.RegistryToken{Token: "SECRET-REG-TOKEN-1"}, nil
	}
	return c.token(n, r)
}
func (c *fakeCtl) PushReport(r *agentv1.PushReport) {
	c.mu.Lock()
	c.pushes = append(c.pushes, r)
	c.mu.Unlock()
}
func (c *fakeCtl) PullReport(r *agentv1.PullReport) {
	c.mu.Lock()
	c.pulls = append(c.pulls, r)
	c.mu.Unlock()
}

func (c *fakeCtl) pushStates() []agentv1.PushReport_State {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []agentv1.PushReport_State
	for _, p := range c.pushes {
		out = append(out, p.GetState())
	}
	return out
}

var wantDigest = "sha256:" + strings.Repeat("a", 64)

func found() *agentv1.EnsureImageAnswer {
	return &agentv1.EnsureImageAnswer{Outcome: agentv1.EnsureImageAnswer_OUTCOME_FOUND, Name: "crsp/std/node-web", ManifestDigest: wantDigest,
		SizeBytes: 3400 << 20, RegistryHost: "ctl.lan:8443"}
}

func notFound(staging bool) *agentv1.EnsureImageAnswer {
	a := &agentv1.EnsureImageAnswer{Outcome: agentv1.EnsureImageAnswer_OUTCOME_NOT_FOUND, RegistryHost: "ctl.lan:8443"}
	if staging {
		a.StagingName, a.TargetName = "crsp/incoming/m1/node-web", "crsp/std/node-web"
	}
	return a
}

type ensureRig struct {
	*rig
	ctl    *fakeCtl
	sleeps []time.Duration
}

func newEnsureRig(t *testing.T, ctl *fakeCtl) *ensureRig {
	t.Helper()
	er := &ensureRig{ctl: ctl}
	clk := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	er.rig = newRig(t, func(c *Config) {
		c.Controller = ctl
		c.Now = func() time.Time { clk = clk.Add(3 * time.Second); return clk }
	})
	er.b.d.Sleep = func(_ context.Context, d time.Duration) { er.sleeps = append(er.sleeps, d) }
	return er
}

func (er *ensureRig) prepare(ctx context.Context) (backend.ImageRef, error) {
	return er.b.PrepareImage(ctx, backend.ImageSpec{OS: "linux", Layers: []string{"node"}})
}

func (er *ensureRig) logText() string { return strings.Join(er.logs, "\n") }

func (er *ensureRig) seed(layers ...string) string {
	tag := ImageTag(er.b.KeyFor(backend.ImageSpec{OS: "linux", Layers: layers}))
	er.f.images[tag] = fakeImg{size: 900 << 20}
	return tag
}

func (er *ensureRig) builds() int { return er.f.count("build") }

// R5.13: the engine has the key, so nothing is asked and nothing is built.
func TestEnsureLocalImageFirst(t *testing.T) {
	ctl := &fakeCtl{ensure: func(int) (*agentv1.EnsureImageAnswer, error) { t.Fatal("asked the controller"); return nil, nil }}
	er := newEnsureRig(t, ctl)
	tag := er.seed("node")
	img, err := er.prepare(context.Background())
	if err != nil || img.ID != tag {
		t.Fatalf("got %v %v", img, err)
	}
	if er.builds() != 0 || er.f.count("pull") != 0 {
		t.Fatalf("built or pulled:\n%s", er.f.text())
	}
	if len(ctl.pulls) != 1 || ctl.pulls[0].GetSource() != agentv1.PullReport_SOURCE_LOCAL {
		t.Fatalf("want one local report, got %v", ctl.pulls)
	}
}

// R5.19: a controller that did not say it has the registry is not asked; the
// image is built as before and nothing is pushed.
func TestEnsureControllerWithoutRegistryBuildsAsBefore(t *testing.T) {
	ctl := &fakeCtl{off: true, ensure: func(int) (*agentv1.EnsureImageAnswer, error) { t.Fatal("asked"); return nil, nil }}
	er := newEnsureRig(t, ctl)
	if _, err := er.prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if er.builds() != 1 || ctl.nEnsure != 0 || len(ctl.pushes) != 0 {
		t.Fatalf("builds=%d asks=%d pushes=%d", er.builds(), ctl.nEnsure, len(ctl.pushes))
	}
}

// R5.13, R5.17: found: a token, a pull by digest, a verify, a tag; no build.
func TestEnsureFoundPullsByDigest(t *testing.T) {
	ctl := &fakeCtl{ensure: func(int) (*agentv1.EnsureImageAnswer, error) { return found(), nil }}
	er := newEnsureRig(t, ctl)
	er.f.pullLines = []string{"aaaaaaaaaaaa: Pulling fs layer", "bbbbbbbbbbbb: Pulling fs layer", "aaaaaaaaaaaa: Pull complete", "bbbbbbbbbbbb: Pull complete"}
	img, err := er.prepare(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	key := er.b.KeyFor(backend.ImageSpec{OS: "linux", Layers: []string{"node"}})
	if img.ID != ImageTag(key) {
		t.Fatalf("image %v", img)
	}
	calls := er.f.text()
	if !strings.Contains(calls, "pull ctl.lan:8443/crsp/std/node-web@"+wantDigest) || !strings.Contains(calls, "tag ctl.lan:8443/crsp/std/node-web@"+wantDigest+" "+ImageTag(key)) {
		t.Fatalf("calls:\n%s", calls)
	}
	if er.builds() != 0 {
		t.Fatal("built")
	}
	if strings.Contains(calls, "SECRET-REG-TOKEN-1") || strings.Contains(er.logText(), "SECRET-REG-TOKEN-1") {
		t.Fatal("the token leaked into a call or the log")
	}
	if ctl.tokenReq[0].GetAction() != agentv1.RegistryTokenRequest_ACTION_PULL || ctl.tokenReq[0].GetNames()[0] != "crsp/std/node-web" {
		t.Fatalf("token request %v", ctl.tokenReq[0])
	}
	if got := ctl.ensured[0]; got.GetKey() != key || got.GetArch() != "amd64" || got.GetRequestId() == "" {
		t.Fatalf("ensure request %v", got)
	}
	first, last := ctl.pulls[0], ctl.pulls[len(ctl.pulls)-1]
	if first.GetState() != agentv1.PullReport_STATE_PULLING || last.GetState() != agentv1.PullReport_STATE_DONE || last.GetSource() != agentv1.PullReport_SOURCE_PULLED || last.GetManifestDigest() != wantDigest {
		t.Fatalf("reports %v", ctl.pulls)
	}
	if len(ctl.pulls) < 3 {
		t.Fatalf("want a progress report between start and end, got %d", len(ctl.pulls))
	}
	if !strings.Contains(er.logText(), "Pulling the job image from the registry, ") || !strings.Contains(er.logText(), " GB") {
		t.Fatalf("log:\n%s", er.logText())
	}
}

// R5.18: a pull whose digest is not the asked one is dropped and the image is built, with a line that says so.
func TestEnsurePullDigestMismatchBuildsLocally(t *testing.T) {
	ctl := &fakeCtl{ensure: func(int) (*agentv1.EnsureImageAnswer, error) { return found(), nil }}
	er := newEnsureRig(t, ctl)
	er.f.pullBadSum = "sha256:" + strings.Repeat("e", 64)
	if _, err := er.prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if er.builds() != 1 || !strings.Contains(er.logText(), "building the image here instead") {
		t.Fatalf("builds=%d\n%s", er.builds(), er.logText())
	}
	if len(ctl.pushes) != 0 {
		t.Fatal("pushed after a failed pull; the answer had no staging name")
	}
	if last := ctl.pulls[len(ctl.pulls)-2]; last.GetState() != agentv1.PullReport_STATE_FAILED {
		t.Fatalf("want a failed pull report, got %v", ctl.pulls)
	}
}

// R5.18: a transfer that fails after its retries falls back to a build.
func TestEnsurePullTransferFailsAfterRetries(t *testing.T) {
	ctl := &fakeCtl{ensure: func(int) (*agentv1.EnsureImageAnswer, error) { return found(), nil }}
	er := newEnsureRig(t, ctl)
	er.f.pullFail = 99
	if _, err := er.prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(er.f.text(), "pull ctl.lan:8443"); n != pullTries {
		t.Fatalf("want %d pulls, got %d", pullTries, n)
	}
	if er.builds() != 1 {
		t.Fatalf("builds=%d", er.builds())
	}
}

// A refused token is a failed pull: build, do not wait.
func TestEnsureTokenRefusedBuilds(t *testing.T) {
	ctl := &fakeCtl{ensure: func(int) (*agentv1.EnsureImageAnswer, error) { return found(), nil },
		token: func(int, *agentv1.RegistryTokenRequest) (*agentv1.RegistryToken, error) {
			return &agentv1.RegistryToken{Refused: "no"}, nil
		}}
	er := newEnsureRig(t, ctl)
	if _, err := er.prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if er.builds() != 1 || er.f.count("pull") != 0 {
		t.Fatalf("calls:\n%s", er.f.text())
	}
}

// R5.15, R5.16: not found with a staging name: build, return at once, push in the background.
func TestEnsureNotFoundBuildsThenPushesInBackground(t *testing.T) {
	gate := make(chan struct{})
	ctl := &fakeCtl{ensure: func(int) (*agentv1.EnsureImageAnswer, error) { return notFound(true), nil },
		token: func(_ int, r *agentv1.RegistryTokenRequest) (*agentv1.RegistryToken, error) {
			if r.GetAction() == agentv1.RegistryTokenRequest_ACTION_PUSH {
				<-gate
			}
			return &agentv1.RegistryToken{Token: "SECRET-REG-TOKEN-1"}, nil
		}}
	er := newEnsureRig(t, ctl)
	img, err := er.prepare(context.Background()) // returns while the push waits for its token
	if err != nil {
		t.Fatal(err)
	}
	key := er.b.KeyFor(backend.ImageSpec{OS: "linux", Layers: []string{"node"}})
	if img.ID != ImageTag(key) || er.builds() != 1 {
		t.Fatalf("img %v builds %d", img, er.builds())
	}
	if er.f.count("push") != 0 {
		t.Fatal("the push ran before the job image returned")
	}
	close(gate)
	er.b.pushWG.Wait()
	calls := er.f.text()
	if !strings.Contains(calls, "push ctl.lan:8443/crsp/incoming/m1/node-web:"+key) {
		t.Fatalf("calls:\n%s", calls)
	}
	if strings.Contains(calls, "SECRET-REG-TOKEN-1") {
		t.Fatal("token in a call")
	}
	st := ctl.pushStates()
	if st[0] != agentv1.PushReport_STATE_PUSHING || st[len(st)-1] != agentv1.PushReport_STATE_PUSHED {
		t.Fatalf("states %v", st)
	}
	last := ctl.pushes[len(ctl.pushes)-1]
	if last.GetManifestDigest() != "sha256:"+strings.Repeat("c", 64) || last.GetTargetName() != "crsp/std/node-web" || last.GetKey() != key {
		t.Fatalf("report %v", last)
	}
	if _, ok := er.f.images[ImageTag(key)]; !ok {
		t.Fatal("the local image is gone")
	}
	if last := ctl.pulls[len(ctl.pulls)-1]; last.GetSource() != agentv1.PullReport_SOURCE_BUILT {
		t.Fatalf("want a built report, got %v", last)
	}
}

// R5.20: a controller without a registry says not found with no staging name: build, push nothing.
func TestEnsureNotFoundWithoutStagingPushesNothing(t *testing.T) {
	ctl := &fakeCtl{ensure: func(int) (*agentv1.EnsureImageAnswer, error) { return notFound(false), nil }}
	er := newEnsureRig(t, ctl)
	if _, err := er.prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	er.b.pushWG.Wait()
	if er.builds() != 1 || len(ctl.pushes) != 0 || ctl.nToken != 0 {
		t.Fatalf("builds=%d pushes=%d tokens=%d", er.builds(), len(ctl.pushes), ctl.nToken)
	}
}

// R5.14: no answer: the job waits, says so with the time waited, backs off,
// and builds only after a clear "not found".
func TestEnsureWaitsForTheController(t *testing.T) {
	var er *ensureRig
	ctl := &fakeCtl{}
	ctl.ensure = func(n int) (*agentv1.EnsureImageAnswer, error) {
		if er.builds() != 0 {
			t.Fatal("built while the controller did not answer")
		}
		if n <= 3 {
			return nil, ErrNoController
		}
		return notFound(false), nil
	}
	er = newEnsureRig(t, ctl)
	if _, err := er.prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ctl.nEnsure != 4 || er.builds() != 1 {
		t.Fatalf("asks=%d builds=%d", ctl.nEnsure, er.builds())
	}
	want := []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second}
	if len(er.sleeps) != 3 || er.sleeps[0] != want[0] || er.sleeps[1] != want[1] || er.sleeps[2] != want[2] {
		t.Fatalf("back-off %v", er.sleeps)
	}
	n := strings.Count(er.logText(), WaitingText+" (waited ")
	if n != 3 {
		t.Fatalf("want 3 wait lines, got %d:\n%s", n, er.logText())
	}
	// The time waited grows from line to line.
	if !strings.Contains(er.logText(), "(waited 3 s)") || !strings.Contains(er.logText(), "(waited 9 s)") {
		t.Fatalf("log:\n%s", er.logText())
	}
}

// R5.14: a job that is cancelled while the controller is away never builds.
func TestEnsureWaitEndsWithTheContextAndNeverBuilds(t *testing.T) {
	ctl := &fakeCtl{ensure: func(int) (*agentv1.EnsureImageAnswer, error) { return nil, ErrNoController }}
	er := newEnsureRig(t, ctl)
	ctx, cancel := context.WithCancel(context.Background())
	er.b.d.Sleep = func(context.Context, time.Duration) {
		er.sleeps = append(er.sleeps, 0)
		if len(er.sleeps) == 5 {
			cancel()
		}
	}
	_, err := er.prepare(ctx)
	if err == nil || er.builds() != 0 {
		t.Fatalf("err=%v builds=%d", err, er.builds())
	}
}

// The back-off caps at a minute.
func TestBackoffCaps(t *testing.T) {
	if backoff(0) != 2*time.Second || backoff(5) != time.Minute || backoff(50) != time.Minute {
		t.Fatal("back-off")
	}
}

// R5.15: busy is asked again after retry_after_ms; it is never built on.
func TestEnsureBusyAsksAgain(t *testing.T) {
	var er *ensureRig
	ctl := &fakeCtl{}
	ctl.ensure = func(n int) (*agentv1.EnsureImageAnswer, error) {
		if er.builds() != 0 {
			t.Fatal("built on busy")
		}
		if n == 1 {
			return &agentv1.EnsureImageAnswer{Outcome: agentv1.EnsureImageAnswer_OUTCOME_BUSY, RetryAfterMs: 1500}, nil
		}
		return found(), nil
	}
	er = newEnsureRig(t, ctl)
	if _, err := er.prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(er.sleeps) != 1 || er.sleeps[0] != 1500*time.Millisecond || er.builds() != 0 || ctl.nEnsure != 2 {
		t.Fatalf("sleeps=%v builds=%d asks=%d", er.sleeps, er.builds(), ctl.nEnsure)
	}
}

// An answer with no clear outcome is not "not in the registry": wait, do not build.
func TestEnsureUnclearAnswerWaits(t *testing.T) {
	var er *ensureRig
	ctl := &fakeCtl{}
	ctl.ensure = func(n int) (*agentv1.EnsureImageAnswer, error) {
		if n == 1 {
			return &agentv1.EnsureImageAnswer{}, nil
		}
		return notFound(false), nil
	}
	er = newEnsureRig(t, ctl)
	if _, err := er.prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ctl.nEnsure != 2 || !strings.Contains(er.logText(), WaitingText) {
		t.Fatalf("asks=%d\n%s", ctl.nEnsure, er.logText())
	}
}

// R5.16: while the controller is offline a push waits (state waiting_controller) and goes through later.
func TestPushWaitsForTheControllerThenPushes(t *testing.T) {
	ctl := &fakeCtl{ensure: func(int) (*agentv1.EnsureImageAnswer, error) { return notFound(true), nil },
		token: func(n int, _ *agentv1.RegistryTokenRequest) (*agentv1.RegistryToken, error) {
			if n >= 1 && n <= 2 {
				return nil, ErrNoController
			}
			return &agentv1.RegistryToken{Token: "SECRET-REG-TOKEN-1"}, nil
		}}
	er := newEnsureRig(t, ctl)
	if _, err := er.prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	er.b.pushWG.Wait()
	var waiting int
	for _, s := range ctl.pushStates() {
		if s == agentv1.PushReport_STATE_WAITING_CONTROLLER {
			waiting++
		}
	}
	st := ctl.pushStates()
	if waiting != 2 || st[len(st)-1] != agentv1.PushReport_STATE_PUSHED {
		t.Fatalf("states %v", st)
	}
}

// R5.16: a failed push keeps the local image and is retried with back-off, then reported failed.
func TestPushFailureKeepsImageAndRetries(t *testing.T) {
	ctl := &fakeCtl{ensure: func(int) (*agentv1.EnsureImageAnswer, error) { return notFound(true), nil }}
	er := newEnsureRig(t, ctl)
	er.f.pushFail = 99
	img, err := er.prepare(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	er.b.pushWG.Wait()
	if n := strings.Count(er.f.text(), "push ctl.lan"); n != pushTries {
		t.Fatalf("want %d pushes, got %d", pushTries, n)
	}
	st := ctl.pushStates()
	if st[len(st)-1] != agentv1.PushReport_STATE_FAILED || ctl.pushes[len(ctl.pushes)-1].GetError() == "" {
		t.Fatalf("states %v", st)
	}
	if _, ok := er.f.images[img.ID]; !ok {
		t.Fatal("the local image was removed")
	}
}

// A refused push token is not retried.
func TestPushRefusedIsNotRetried(t *testing.T) {
	ctl := &fakeCtl{ensure: func(int) (*agentv1.EnsureImageAnswer, error) { return notFound(true), nil },
		token: func(int, *agentv1.RegistryTokenRequest) (*agentv1.RegistryToken, error) {
			return &agentv1.RegistryToken{Refused: "over budget"}, nil
		}}
	er := newEnsureRig(t, ctl)
	if _, err := er.prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	er.b.pushWG.Wait()
	if ctl.nToken != 1 || ctl.pushStates()[len(ctl.pushes)-1] != agentv1.PushReport_STATE_FAILED || er.f.count("push") != 0 {
		t.Fatalf("tokens=%d states=%v", ctl.nToken, ctl.pushStates())
	}
}

// R5.17: the progress parser adds up bytes when the engine prints them and
// scales by finished layers when it does not.
func TestPullProgress(t *testing.T) {
	p := &pullProgress{total: 4000}
	for _, l := range []string{"aaaaaaaaaaaa: Pulling fs layer", "bbbbbbbbbbbb: Pulling fs layer", "aaaaaaaaaaaa: Pull complete"} {
		p.feed(l)
	}
	if d, tot := p.bytes(); d != 2000 || tot != 4000 {
		t.Fatalf("layers: %d of %d", d, tot)
	}
	q := &pullProgress{}
	q.feed("aaaaaaaaaaaa: Downloading [==>   ]  10MB/40MB")
	q.feed("bbbbbbbbbbbb: Downloading [==>   ]  20MB/60MB")
	if d, tot := q.bytes(); d != 30_000_000 || tot != 100_000_000 {
		t.Fatalf("bytes: %d of %d", d, tot)
	}
	if got := sizeText(2_100_000_000, 3_400_000_000, 100*time.Second); got != ", 2.1 of 3.4 GB, about 1 min left" {
		t.Fatalf("got %q", got)
	}
	if got := sizeText(3_000_000_000, 3_400_000_000, 10*time.Second); !strings.Contains(got, "less than a minute left") {
		t.Fatalf("got %q", got)
	}
	if sizeText(0, 0, 0) != "" {
		t.Fatal("no size, no text")
	}
}
