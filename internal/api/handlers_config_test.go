package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func planBody(t *testing.T, file string) string {
	t.Helper()
	b, err := json.Marshal(map[string]string{"file": file})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestConfigExportPlanApply(t *testing.T) {
	e := newOpsEnv(t)
	rec := e.do("GET", "/api/v1/config/export", "")
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/yaml" {
		t.Fatalf("export %d %s", rec.Code, rec.Body)
	}
	if cd := rec.Header().Get("Content-Disposition"); cd != `attachment; filename="crsp-setup-2026-11-14-1005.yaml"` {
		t.Fatalf("Content-Disposition %q", cd)
	}
	file := rec.Body.String()
	for _, want := range []string{"format: crsp-config", "version: 1", "settings:", "pool:", "queue:", "retention_audit_days: 400"} {
		if !strings.Contains(file, want) {
			t.Errorf("export lacks %q:\n%s", want, file)
		}
	}

	// The unchanged file plans to nothing.
	rec = e.do("POST", "/api/v1/config/plans", planBody(t, file))
	if rec.Code != 201 || !strings.Contains(rec.Body.String(), `"changes":[]`) {
		t.Fatalf("plan of the same file: %d %s", rec.Code, rec.Body)
	}

	// An edited file lists the change and changes nothing.
	edited := strings.Replace(file, "retention_audit_days: 400", "retention_audit_days: 500", 1)
	rec = e.do("POST", "/api/v1/config/plans", planBody(t, edited))
	var plan struct {
		ID      string `json:"id"`
		Changes []struct {
			Section, Kind string
			Fields        []struct {
				Field    string
				Old, New float64
			}
		}
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &plan); err != nil || rec.Code != 201 || plan.ID == "" || len(plan.Changes) != 1 {
		t.Fatalf("plan: %d %s", rec.Code, rec.Body)
	}
	if f := plan.Changes[0].Fields[0]; plan.Changes[0].Section != "settings" || f.Field != "retention_audit_days" || f.Old != 400 || f.New != 500 {
		t.Fatalf("change = %+v", plan.Changes[0])
	}
	if rec := e.do("GET", "/api/v1/settings", ""); !strings.Contains(rec.Body.String(), `"retention_audit_days":400`) {
		t.Fatalf("a plan changed the setting: %s", rec.Body)
	}

	// Apply writes it and audits it.
	rec = e.do("POST", "/api/v1/config/plans/"+plan.ID+"/apply", "")
	if rec.Code != 200 {
		t.Fatalf("apply %d %s", rec.Code, rec.Body)
	}
	if rec := e.do("GET", "/api/v1/settings", ""); !strings.Contains(rec.Body.String(), `"retention_audit_days":500`) {
		t.Fatalf("apply did not change the setting: %s", rec.Body)
	}
	if rec := e.do("GET", "/api/v1/audit?action=config.apply", ""); !strings.Contains(rec.Body.String(), "config.apply") {
		t.Fatalf("the change was not audited: %s", rec.Body)
	}
	// The same plan cannot run twice.
	if rec := e.do("POST", "/api/v1/config/plans/"+plan.ID+"/apply", ""); rec.Code != 404 {
		t.Fatalf("second apply %d %s", rec.Code, rec.Body)
	}
}

func TestConfigApplyRefusesAStalePlan(t *testing.T) {
	e := newOpsEnv(t)
	file := e.do("GET", "/api/v1/config/export", "").Body.String()
	edited := strings.Replace(file, "retention_audit_days: 400", "retention_audit_days: 500", 1)
	var plan struct{ ID string }
	_ = json.Unmarshal(e.do("POST", "/api/v1/config/plans", planBody(t, edited)).Body.Bytes(), &plan)
	e.do("PATCH", "/api/v1/settings", `{"max_event_streams":60}`) // the setup changed after the plan
	rec := e.do("POST", "/api/v1/config/plans/"+plan.ID+"/apply", "")
	if rec.Code != 409 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	assertEnvelope(t, rec, CodeConflict)
}

func TestConfigPlanRefusesASecretAndAnythingThatIsNotAConfigFile(t *testing.T) {
	e := newOpsEnv(t)
	rec := e.do("POST", "/api/v1/config/plans", planBody(t, "format: crsp-config\nversion: 1\nsettings:\n  webhook_secret: abc\n"))
	if rec.Code != 201 || strings.Contains(rec.Body.String(), `"id"`) || !strings.Contains(rec.Body.String(), "webhook_secret") || strings.Contains(rec.Body.String(), "abc") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	rec = e.do("POST", "/api/v1/config/plans", planBody(t, "hello: world\n"))
	if rec.Code != 400 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	assertEnvelope(t, rec, CodeBadRequest)
}
