package api

import (
	"strings"
	"testing"
)

func TestPoolSettingsGetPutAndRefuse(t *testing.T) {
	e := newOpsEnv(t)
	rec := e.do("GET", "/api/v1/settings/pool", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"drain_timeout_minutes":60`) || !strings.Contains(rec.Body.String(), `"budget_memory_pct":85`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	body := `{"budget_memory_pct":70,"budget_cpu_pct":50,"size_slots_from_machine":false,"default_job_slots":4,"drain_timeout_minutes":120}`
	rec = e.do("PUT", "/api/v1/settings/pool", body)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"default_job_slots":4`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if rec := e.do("GET", "/api/v1/settings/pool", ""); !strings.Contains(rec.Body.String(), `"budget_cpu_pct":50`) {
		t.Fatalf("not persisted: %s", rec.Body)
	}
	rec = e.do("PUT", "/api/v1/settings/pool", `{"budget_memory_pct":96,"budget_cpu_pct":9,"size_slots_from_machine":true,"default_job_slots":65,"drain_timeout_minutes":4}`)
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "between 10 and 95") || !strings.Contains(rec.Body.String(), "between 5 and 1440") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	assertEnvelope(t, rec, CodeBadRequest)
	if rec := e.do("GET", "/api/v1/settings/pool", ""); !strings.Contains(rec.Body.String(), `"budget_memory_pct":70`) {
		t.Fatalf("a refused change was applied: %s", rec.Body)
	}
	if rec := e.do("GET", "/api/v1/audit?action=settings.pool.update", ""); strings.Count(rec.Body.String(), `"result":"done"`) != 1 || strings.Count(rec.Body.String(), `"result":"refused"`) != 1 {
		t.Fatalf("audit: %s", rec.Body)
	}
}

func TestQueueSettingsGetPutAndRefuse(t *testing.T) {
	e := newOpsEnv(t)
	rec := e.do("GET", "/api/v1/settings/queue", "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"order":["priority","event_class","fair_share","queued_at"]`) || !strings.Contains(rec.Body.String(), `"no_runner_wait_minutes":2`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	rec = e.do("PUT", "/api/v1/settings/queue", `{"order":["queued_at","fair_share","event_class","priority"],"fair_share":false,"no_runner_wait_minutes":5}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"order":["queued_at","fair_share","event_class","priority"]`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	for name, body := range map[string]string{
		"duplicate": `{"order":["priority","priority","fair_share","queued_at"],"fair_share":true,"no_runner_wait_minutes":2}`,
		"unknown":   `{"order":["priority","event_class","fair_share","size"],"fair_share":true,"no_runner_wait_minutes":2}`,
		"short":     `{"order":["priority"],"fair_share":true,"no_runner_wait_minutes":2}`,
		"wait":      `{"order":["priority","event_class","fair_share","queued_at"],"fair_share":true,"no_runner_wait_minutes":61}`,
	} {
		rec := e.do("PUT", "/api/v1/settings/queue", body)
		if rec.Code != 400 {
			t.Fatalf("%s: %d %s", name, rec.Code, rec.Body)
		}
		assertEnvelope(t, rec, CodeBadRequest)
	}
	if rec := e.do("GET", "/api/v1/settings/queue", ""); !strings.Contains(rec.Body.String(), `"no_runner_wait_minutes":5`) {
		t.Fatalf("a refused change was applied: %s", rec.Body)
	}
}
