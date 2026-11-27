package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeScaleSet is a fake of GitHub's scale-set service, with the registration
// route and the message queue in one server.
type fakeScaleSet struct {
	mu       sync.Mutex
	srv      *httptest.Server
	msgs     []fakeMsg
	acquired [][]int64
	acked    []int64
	jitNames []string
	closed   bool
	badQueue bool // answer the queue with a shape the client does not know
	regAuth  string
	made     bool
}

type fakeMsg struct {
	id   int64
	body string
}

const fakeJITValue = "FAKE-SCALESET-JIT-0001"

func newFakeScaleSet(t *testing.T) *fakeScaleSet {
	t.Helper()
	f := &fakeScaleSet{}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeScaleSet) queue(id int64, jobs ...map[string]any) {
	b, _ := json.Marshal(jobs)
	f.mu.Lock()
	f.msgs = append(f.msgs, fakeMsg{id: id, body: string(b)})
	f.mu.Unlock()
}

func (f *fakeScaleSet) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := r.URL.Path
	reply := func(code int, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		if v != nil {
			_ = json.NewEncoder(w).Encode(v)
		}
	}
	switch {
	case p == "/actions/runner-registration":
		f.regAuth = r.Header.Get("Authorization")
		reply(200, map[string]string{"url": f.srv.URL + "/svc", "token": "admin-bearer"})
	case p == "/svc/_apis/runtime/runnerscalesets" && r.Method == "GET":
		if f.made {
			reply(200, map[string]any{"count": 1, "value": []map[string]any{{"id": 5, "name": r.URL.Query().Get("name")}}})
			return
		}
		reply(200, map[string]any{"count": 0, "value": []any{}})
	case p == "/svc/_apis/runtime/runnerscalesets" && r.Method == "POST":
		f.made = true
		reply(200, map[string]any{"id": 5})
	case p == "/svc/_apis/runtime/runnerscalesets/5/sessions" && r.Method == "POST":
		reply(200, map[string]any{"sessionId": "s-1", "messageQueueUrl": f.srv.URL + "/queue", "messageQueueAccessToken": "queue-bearer"})
	case strings.HasPrefix(p, "/svc/_apis/runtime/runnerscalesets/5/sessions/") && r.Method == "DELETE":
		f.closed = true
		reply(204, nil)
	case p == "/queue" && r.Method == "GET":
		if r.Header.Get("Authorization") != "Bearer queue-bearer" || r.URL.Query().Get("sessionId") != "s-1" {
			reply(401, nil)
			return
		}
		if f.badQueue {
			reply(200, map[string]any{"messageType": "RunnerScaleSetJobMessages", "body": "not json", "messageId": 1})
			return
		}
		last, _ := strconv.ParseInt(r.URL.Query().Get("lastMessageId"), 10, 64)
		for _, m := range f.msgs {
			if m.id > last {
				reply(200, map[string]any{"messageId": m.id, "messageType": "RunnerScaleSetJobMessages", "body": m.body, "statistics": map[string]any{"totalAssignedJobs": 1}})
				return
			}
		}
		w.WriteHeader(202)
	case strings.HasPrefix(p, "/queue/") && r.Method == "DELETE":
		id, _ := strconv.ParseInt(strings.TrimPrefix(p, "/queue/"), 10, 64)
		f.acked = append(f.acked, id)
		reply(204, nil)
	case p == "/svc/_apis/runtime/runnerscalesets/5/acquirejobs":
		var ids []int64
		_ = json.NewDecoder(r.Body).Decode(&ids)
		f.acquired = append(f.acquired, ids)
		// Give back all but request 99, which another listener took.
		var got []int64
		for _, id := range ids {
			if id != 99 {
				got = append(got, id)
			}
		}
		reply(200, map[string]any{"value": got})
	case p == "/svc/_apis/runtime/runnerscalesets/5/generatejitconfig":
		var in struct {
			Name string `json:"name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		f.jitNames = append(f.jitNames, in.Name)
		reply(200, map[string]any{"runner": map[string]any{"id": 4242}, "encodedJITConfig": fakeJITValue})
	default:
		reply(404, map[string]string{"message": "no route " + p})
	}
}

type fakeTokens struct{}

func (fakeTokens) CreateRegistrationToken(context.Context, int64, RunnerScope) (string, error) {
	return "reg-token", nil
}

func scaleDemand(f *fakeScaleSet) *ScaleSetDemand {
	return &ScaleSetDemand{
		Targets: func(context.Context) ([]ScaleSetTarget, error) {
			return []ScaleSetTarget{{InstallationID: 10, Owner: "luis-mc", Repo: "app"}}, nil
		},
		Tokens: fakeTokens{}, Name: "crsp-pool", APIBase: f.srv.URL, WebBase: "https://github.example",
	}
}

func nextJob(t *testing.T, ch <-chan Job) Job {
	t.Helper()
	select {
	case j, ok := <-ch:
		if !ok {
			t.Fatal("the job stream closed")
		}
		return j
	case <-time.After(5 * time.Second):
		t.Fatal("no job in time")
	}
	return Job{}
}

// R5.35, R5.40: offered jobs are acquired and reported as queued; started and
// finished jobs follow; each message is acknowledged; the JIT config of a
// scale-set job comes from the scale set and masks itself.
func TestScaleSetDemandJobs(t *testing.T) {
	f := newFakeScaleSet(t)
	f.queue(1, map[string]any{"messageType": "JobAvailable", "runnerRequestId": 7, "ownerName": "luis-mc", "repositoryName": "app", "eventName": "push", "requestLabels": []string{"docker"}},
		map[string]any{"messageType": "JobAvailable", "runnerRequestId": 99, "ownerName": "luis-mc", "repositoryName": "app", "eventName": "push"})
	f.queue(2, map[string]any{"messageType": "JobStarted", "runnerRequestId": 7, "runnerName": "crsp-job-7"})
	f.queue(3, map[string]any{"messageType": "JobCompleted", "runnerRequestId": 7, "runnerName": "crsp-job-7", "result": "succeeded"})
	d := scaleDemand(f)
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := d.Jobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	q := nextJob(t, ch)
	if q.ID != 7 || q.Status != "queued" || q.Repository != "luis-mc/app" || q.ScaleSetID != 5 || q.FromFork {
		t.Fatalf("queued job = %+v", q)
	}
	if got := strings.Join(q.Labels, ","); got != "self-hosted,linux,docker" {
		t.Fatalf("labels = %s", got)
	}
	// The JIT config of this job comes from the scale set, under the runner's name.
	jit, err := d.JIT(ctx, q, "crsp-job-7")
	if err != nil {
		t.Fatal(err)
	}
	if jit.Encoded() != fakeJITValue || jit.RunnerID != 4242 {
		t.Fatalf("jit = %v", jit)
	}
	for _, shown := range []string{fmt.Sprintf("%v", jit), fmt.Sprintf("%#v", jit)} {
		if strings.Contains(shown, fakeJITValue) {
			t.Fatalf("the JIT config shows in a print: %s", shown)
		}
	}
	if s := nextJob(t, ch); s.Status != "in_progress" || s.RunnerName != "crsp-job-7" {
		t.Fatalf("started job = %+v", s)
	}
	if c := nextJob(t, ch); c.Status != "completed" || c.Conclusion != "succeeded" {
		t.Fatalf("completed job = %+v", c)
	}
	// The last message is acknowledged right after its job was handed on.
	waitFor(t, func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return len(f.acked) == 3
	})
	cancel()
	for range ch {
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.acquired) != 1 || len(f.acquired[0]) != 2 {
		t.Fatalf("acquire = %v", f.acquired)
	}
	if fmt.Sprint(f.acked) != "[1 2 3]" {
		t.Fatalf("acked = %v", f.acked)
	}
	if !f.closed {
		t.Fatal("the session was not closed")
	}
	if f.regAuth != "RemoteAuth reg-token" {
		t.Fatalf("registration auth = %q", f.regAuth)
	}
	if err := d.Err(); err != nil {
		t.Fatalf("err = %v", err)
	}
}

// A pull request is treated as a fork pull request: the message does not say.
func TestScaleSetPullRequestFailsClosed(t *testing.T) {
	f := newFakeScaleSet(t)
	f.queue(1, map[string]any{"messageType": "JobAvailable", "runnerRequestId": 8, "eventName": "pull_request"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch, err := scaleDemand(f).Jobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if j := nextJob(t, ch); !j.FromFork {
		t.Fatalf("a pull request must count as a fork: %+v", j)
	}
}

// An answer the client does not understand ends the stream with
// ErrScaleSetAPI, so the caller can fall back to polling.
func TestScaleSetAPIChangeEndsStream(t *testing.T) {
	f := newFakeScaleSet(t)
	f.badQueue = true
	d := scaleDemand(f)
	ch, err := d.Jobs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("no job expected")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the stream did not end")
	}
	if !errors.Is(d.Err(), ErrScaleSetAPI) {
		t.Fatalf("err = %v", d.Err())
	}
}

func TestScaleSetNeedsSetup(t *testing.T) {
	if _, err := (&ScaleSetDemand{}).Jobs(context.Background()); err == nil {
		t.Fatal("a source with no setup must refuse to start")
	}
}
