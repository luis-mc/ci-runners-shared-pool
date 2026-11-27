package github

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github/fake"
)

type recEvents struct {
	jobs  []Job
	insts []string
	repos []string
}

func (r *recEvents) Repository(action, name string) { r.repos = append(r.repos, action+" "+name) }

func (r *recEvents) Job(j Job)                       { r.jobs = append(r.jobs, j) }
func (r *recEvents) Installation(e string, _ []byte) { r.insts = append(r.insts, e) }

func newHook(t *testing.T) (*WebhookHandler, *recEvents, []byte) {
	t.Helper()
	secret := []byte("whsec-for-tests")
	seen := map[string]bool{}
	ev := &recEvents{}
	return &WebhookHandler{
		Verifier: func() (WebhookVerifier, error) { return NewWebhookVerifier(secret), nil },
		FirstDelivery: func(id, _ string) (bool, error) {
			if seen[id] {
				return false, nil
			}
			seen[id] = true
			return true, nil
		},
		Events: ev,
	}, ev, secret
}

func deliver(h http.Handler, secret, body []byte, sig, id, event string) int {
	r := httptest.NewRequestWithContext(context.Background(), "POST", "/webhooks/github", strings.NewReader(string(body)))
	if sig == "" && secret != nil {
		sig = Sign(secret, body)
	}
	r.Header.Set("X-Hub-Signature-256", sig)
	r.Header.Set("X-GitHub-Delivery", id)
	r.Header.Set("X-GitHub-Event", event)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w.Code
}

func TestWebhookSignature(t *testing.T) { // R6.5
	h, ev, secret := newHook(t)
	body := fake.WorkflowJobQueuedEvent()
	if c := deliver(h, secret, body, "sha256=deadbeef", "d1", "workflow_job"); c != 401 {
		t.Fatalf("bad signature: %d", c)
	}
	if c := deliver(h, secret, body, "nothex", "d1", "workflow_job"); c != 401 {
		t.Fatalf("malformed signature: %d", c)
	}
	if c := deliver(h, nil, body, "", "d1", "workflow_job"); c != 401 {
		t.Fatalf("no signature: %d", c)
	}
	if len(ev.jobs) != 0 {
		t.Fatal("an unsigned body must not be parsed or acted on")
	}
	if c := deliver(h, secret, body, "", "d1", "workflow_job"); c != 200 || len(ev.jobs) != 1 {
		t.Fatalf("signed: %d %v", c, ev.jobs)
	}
}

func TestWebhookUnsignedBodyIsNotParsed(t *testing.T) { // R6.5
	h, ev, _ := newHook(t)
	if c := deliver(h, nil, []byte("{not json"), "sha256=00", "d9", "workflow_job"); c != 401 {
		t.Fatalf("got %d, want 401 before any parse", c)
	}
	if len(ev.jobs) != 0 {
		t.Fatal("parsed")
	}
}

func TestWebhookRepeatDeliveryIgnored(t *testing.T) { // R5.36
	h, ev, secret := newHook(t)
	body := fake.WorkflowJobQueuedEvent()
	deliver(h, secret, body, "", "same", "workflow_job")
	if c := deliver(h, secret, body, "", "same", "workflow_job"); c != 200 {
		t.Fatalf("repeat: %d", c)
	}
	if len(ev.jobs) != 1 {
		t.Fatalf("job delivered %d times", len(ev.jobs))
	}
}

func TestWebhookWorkflowJobParsed(t *testing.T) { // R5.36
	h, ev, secret := newHook(t)
	deliver(h, secret, fake.WorkflowJobQueuedEvent(), "", "d2", "workflow_job")
	j := ev.jobs[0]
	if j.ID != 9001 || j.Repository != "octo-owner/demo" || j.Status != "queued" || !contains(j.Labels, "crsp-home") || j.Workflow != "ci" {
		t.Fatalf("%+v", j)
	}
}

func TestWebhookInstallationEvents(t *testing.T) { // R5.22
	h, ev, secret := newHook(t)
	if c := deliver(h, secret, []byte(`{"action":"created"}`), "", "i1", "installation"); c != 200 {
		t.Fatal(c)
	}
	deliver(h, secret, []byte(`{"action":"added"}`), "", "i2", "installation_repositories")
	if len(ev.insts) != 2 {
		t.Fatalf("%v", ev.insts)
	}
	if c := deliver(h, secret, []byte(`{}`), "", "i3", "ping"); c != 200 {
		t.Fatal(c)
	}
}

func TestWebhookBodyCap(t *testing.T) { // R6.5
	h, _, secret := newHook(t)
	body := make([]byte, MaxWebhookBody+1)
	if c := deliver(h, secret, body, "", "big", "workflow_job"); c != http.StatusRequestEntityTooLarge {
		t.Fatalf("got %d", c)
	}
}

func TestWebhookMissingHeaders(t *testing.T) {
	h, _, secret := newHook(t)
	if c := deliver(h, secret, []byte(`{}`), "", "", "workflow_job"); c != 400 {
		t.Fatalf("got %d", c)
	}
}

func TestVerifierEmptySecretRefuses(t *testing.T) {
	if NewWebhookVerifier(nil).Verify([]byte("x"), Sign(nil, []byte("x"))) {
		t.Fatal("an empty secret must verify nothing")
	}
}

// CRSP82: the repository event reaches the receiver with its action.
func TestWebhookRepositoryEvent(t *testing.T) {
	h, ev, secret := newHook(t)
	body := []byte(`{"action":"publicized","repository":{"full_name":"alice/secret"}}`)
	if c := deliver(h, secret, body, "", "r1", "repository"); c != 200 {
		t.Fatalf("status %d", c)
	}
	if len(ev.repos) != 1 || ev.repos[0] != "publicized alice/secret" {
		t.Fatalf("got %v", ev.repos)
	}
	if c := deliver(h, secret, []byte(`{"action":"publicized"}`), "", "r2", "repository"); c != 400 {
		t.Fatalf("an event with no repository: %d", c)
	}
}

// CRSP82: a job from a webhook is partial: its fork flag is not known.
func TestWebhookJobIsPartial(t *testing.T) {
	j, err := ParseWorkflowJobEvent(fake.WorkflowJobQueuedEvent())
	if err != nil {
		t.Fatal(err)
	}
	if !j.Partial || j.FromFork {
		t.Fatalf("partial %v fork %v", j.Partial, j.FromFork)
	}
}

func TestOnDeliverySeesRepositoryJobAndUse(t *testing.T) { // RES14
	type seen struct {
		repo string
		job  int64
		used bool
	}
	var got []seen
	h := &WebhookHandler{
		Verifier:      func() (WebhookVerifier, error) { return NewWebhookVerifier([]byte("s")), nil },
		FirstDelivery: func(string, string) (bool, error) { return true, nil },
		Events:        nopEvents{},
		OnDelivery:    func(r string, id int64, used bool) { got = append(got, seen{r, id, used}) },
	}
	post := func(body string) {
		req := httptest.NewRequestWithContext(context.Background(), "POST", "/", strings.NewReader(body))
		req.Header.Set("X-Hub-Signature-256", Sign([]byte("s"), []byte(body)))
		req.Header.Set("X-GitHub-Delivery", "d")
		req.Header.Set("X-GitHub-Event", "workflow_job")
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	post(`{"action":"queued","workflow_job":{"id":5,"status":"queued"},"repository":{"full_name":"a/b"}}`)
	post(`{"action":"queued","workflow_job":{"id":0},"repository":{"full_name":"a/b"}}`)
	if len(got) != 2 || got[0] != (seen{"a/b", 5, true}) || got[1] != (seen{"a/b", 0, false}) {
		t.Fatalf("got %+v", got)
	}
}

type nopEvents struct{}

func (nopEvents) Job(Job)                     {}
func (nopEvents) Installation(string, []byte) {}
