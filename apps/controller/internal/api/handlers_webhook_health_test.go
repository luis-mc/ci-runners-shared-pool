package api

import (
	"strings"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/webhookhealth"
)

func TestRepositoryShowsWebhookHealthAndSwitchesBack(t *testing.T) { // RES14
	h := newHarness(t)
	owner, csrf := h.setup(1, "alice")
	hdr := map[string]string{"X-CSRF-Token": csrf}
	h.fake.AddInstall(10, "alice", "User", 1)
	h.fake.AddRepo(100, 10, "alice", "app", true, 1)
	_, _ = h.do("GET", "/api/v1/github/repositories", nil, owner, nil)

	_, b := h.do("GET", "/api/v1/repositories/100", nil, owner, nil)
	if !strings.Contains(string(b), `"webhook":{"failures_in_a_row":0,"mode":"webhook"}`) {
		t.Fatalf("no delivery yet: %s", b)
	}
	for range webhookhealth.FailuresToPoll {
		h.health.Record("alice/app", webhookhealth.ResultFailed)
	}
	_, b = h.do("GET", "/api/v1/repositories/100", nil, owner, nil)
	for _, want := range []string{`"mode":"polling"`, `"last_result":"failed"`, `"last_delivery_at"`, `"polling_since"`} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("missing %s in %s", want, b)
		}
	}
	res, b := h.do("PUT", "/api/v1/repositories/100/webhook-mode", map[string]any{"mode": "webhook"}, owner, hdr)
	if res.StatusCode != 200 || !strings.Contains(string(b), `"mode":"webhook"`) || strings.Contains(string(b), "polling_since") {
		t.Fatalf("switch back: %d %s", res.StatusCode, b)
	}
	if res, _ = h.do("PUT", "/api/v1/repositories/100/webhook-mode", map[string]any{"mode": "polling"}, owner, hdr); res.StatusCode != 400 {
		t.Fatalf("polling cannot be chosen: %d", res.StatusCode)
	}
	if res, _ = h.do("PUT", "/api/v1/repositories/999/webhook-mode", map[string]any{"mode": "webhook"}, owner, hdr); res.StatusCode != 404 {
		t.Fatalf("unknown repository: %d", res.StatusCode)
	}
	if res, _ = h.do("PUT", "/api/v1/repositories/100/webhook-mode", map[string]any{"mode": "webhook"}, owner, nil); res.StatusCode != 403 {
		t.Fatalf("a change needs the CSRF token: %d", res.StatusCode)
	}
}

func TestRepositoryTellsWhenTheAppHasNoWebhook(t *testing.T) { // OPS22
	h := newHarness(t)
	owner, _ := h.setup(1, "alice")
	h.fake.AddInstall(10, "alice", "User", 1)
	h.fake.AddRepo(100, 10, "alice", "app", true, 1)
	_, _ = h.do("GET", "/api/v1/github/repositories", nil, owner, nil)
	h.health.HooksOn = func() bool { return false }
	_, b := h.do("GET", "/api/v1/repositories/100", nil, owner, nil)
	if !strings.Contains(string(b), `"webhooks_off":true`) {
		t.Fatalf("no webhooks_off: %s", b)
	}
}

func TestAppWebhookEndpoints(t *testing.T) { // OPS25, R5.51
	h := newHarness(t)
	owner, csrf := h.setup(1, "alice")
	hdr := map[string]string{"X-CSRF-Token": csrf}
	res, b := h.do("DELETE", "/api/v1/app/webhook", nil, owner, hdr)
	if res.StatusCode != 200 || !strings.Contains(string(b), `"state":"off"`) || h.fake.HookPatches != 1 {
		t.Fatalf("turning off: %d %s (patches %d)", res.StatusCode, b, h.fake.HookPatches)
	}
	if res, b = h.do("GET", "/api/v1/app/webhook", nil, owner, nil); res.StatusCode != 200 || !strings.Contains(string(b), `"state":"off"`) {
		t.Fatalf("off: %d %s", res.StatusCode, b)
	}
	for _, addr := range []string{"http://hooks.example.com", "https://192.168.1.4", "https://crsp.local", "nonsense"} {
		res, b = h.do("PUT", "/api/v1/app/webhook", map[string]any{"address": addr}, owner, hdr)
		if res.StatusCode != 400 {
			t.Fatalf("%s must be refused: %d %s", addr, res.StatusCode, b)
		}
	}
	if res, _ = h.do("PUT", "/api/v1/app/webhook", map[string]any{"address": "https://hooks.example.com"}, owner, nil); res.StatusCode != 403 {
		t.Fatalf("a change needs the CSRF token: %d", res.StatusCode)
	}
}
