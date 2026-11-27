package api

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/events"
)

// ConfigExporter writes the configuration export (web-console R5.29, operations
// R5.26): YAML with no secret. Nil answers 501 like an unbuilt operation.
type ConfigExporter interface {
	YAML(ctx context.Context) ([]byte, error)
}

// fileStamp is the stamp every export and backup file carries: local time to
// the minute, for example crsp-audit-2026-11-14-1005.csv (UX-39).
const fileStamp = "2006-01-02-1504"

func (s *server) now() time.Time {
	if s.opts.Now != nil {
		return s.opts.Now()
	}
	return time.Now()
}

// exportFile is a file download answer of the exports. The generated JSON
// answer would write a file as a quoted string, so this one writes the bytes.
type exportFile struct {
	name        string
	contentType string
	body        []byte
}

func (f exportFile) write(w http.ResponseWriter) error {
	w.Header().Set("Content-Type", f.contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", f.name))
	w.Header().Set("Content-Length", fmt.Sprint(len(f.body)))
	w.WriteHeader(http.StatusOK)
	_, err := w.Write(f.body)
	return err
}

type auditExportAnswer struct{ exportFile }

func (a auditExportAnswer) VisitExportAuditEventsResponse(w http.ResponseWriter) error {
	return a.write(w)
}

type configExportAnswer struct{ exportFile }

func (a configExportAnswer) VisitExportConfigResponse(w http.ResponseWriter) error {
	return a.write(w)
}

// ExportAuditEvents implements GET /api/v1/exports/audit: every row that matches
// the filters of the list, newest first, as csv or json.
func (s *server) ExportAuditEvents(ctx context.Context, req gen.ExportAuditEventsRequestObject) (gen.ExportAuditEventsResponseObject, error) {
	fail := func(f opsFailure) (gen.ExportAuditEventsResponseObject, error) {
		return gen.ExportAuditEventsdefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	if s.opts.Audit == nil {
		return fail(unavailable())
	}
	p := req.Params
	str := func(v *string) string {
		if v == nil {
			return ""
		}
		return *v
	}
	f := audit.Filter{Actor: str(p.Actor), Action: str(p.Action), TargetType: str(p.TargetType), TargetID: str(p.TargetId), Limit: audit.MaxPage}
	if p.From != nil {
		f.From = *p.From
	}
	if p.To != nil {
		f.To = *p.To
	}
	asJSON := p.Format != nil && *p.Format == gen.ExportAuditEventsParamsFormatJson
	var events []audit.Event
	for {
		page, err := s.opts.Audit.List(ctx, f)
		if err != nil {
			return fail(s.opsError(err))
		}
		events = append(events, page.Items...)
		if page.NextCursor == "" {
			break
		}
		f.Cursor = page.NextCursor
	}

	stamp := s.now().Format(fileStamp)
	if asJSON {
		items := make([]gen.AuditEvent, 0, len(events))
		for _, e := range events {
			items = append(items, toGenAuditEvent(e))
		}
		body, err := json.MarshalIndent(items, "", "  ")
		if err != nil {
			return fail(s.opsError(err))
		}
		return auditExportAnswer{exportFile{name: "crsp-audit-" + stamp + ".json", contentType: "application/json", body: append(body, '\n')}}, nil
	}
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{"id", "at", "actor", "source", "action", "target_type", "target_id", "result", "summary"})
	for _, e := range events {
		_ = w.Write([]string{
			fmt.Sprint(e.ID), e.At.UTC().Format(time.RFC3339), csvSafe(e.Actor), e.Source, e.Action,
			e.TargetType, csvSafe(e.TargetID), e.Result, csvSafe(e.Summary),
		})
	}
	w.Flush()
	return auditExportAnswer{exportFile{name: "crsp-audit-" + stamp + ".csv", contentType: "text/csv", body: buf.Bytes()}}, nil
}

// csvSafe stops a spreadsheet from running a cell as a formula.
func csvSafe(v string) string {
	if v != "" && bytes.IndexByte([]byte("=+-@\t\r"), v[0]) >= 0 {
		return "'" + v
	}
	return v
}

// ExportConfig implements GET /api/v1/exports/config (admin).
func (s *server) ExportConfig(ctx context.Context, _ gen.ExportConfigRequestObject) (gen.ExportConfigResponseObject, error) {
	if s.opts.Config == nil {
		f := unavailable()
		return gen.ExportConfigdefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	body, err := s.opts.Config.YAML(ctx)
	if err != nil {
		f := s.opsError(err)
		return gen.ExportConfigdefaultJSONResponse{StatusCode: f.status, Body: f.body}, nil
	}
	if s.opts.Audit != nil {
		_ = s.opts.Audit.Record(ctx, audit.ActorFrom(ctx), "config.export", audit.Target{Type: "config"},
			map[string]any{"summary": "exported the configuration without secrets"})
	}
	return configExportAnswer{exportFile{name: "crsp-config-" + s.now().Format(fileStamp) + ".yaml", contentType: "application/yaml", body: body}}, nil
}

// publishPlan sends the plan as a plan.progress event, when a hub is wired.
func (s *server) publishPlan(p gen.Plan) {
	if s.opts.Events == nil {
		return
	}
	if b, err := json.Marshal(p); err == nil {
		s.opts.Events.Publish(events.PlanProgress, b)
	}
}
