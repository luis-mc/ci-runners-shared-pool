package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machinehealth"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/notify"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/update"
)

// notifySources are the places the notifier reads facts from (notifications R5.1, section 3). Each one reads
// another area through its public interface only; none of them is changed. A source that fails changes nothing.
type notifySources struct {
	db       *store.Store
	machines *machines.Service
	health   *machinehealth.Service
	doctor   interface {
		Findings(ctx context.Context, severity, group string) (gen.DoctorFindingList, error)
	}
	updates interface {
		Status(ctx context.Context) update.Status
	}
	now func() time.Time
	// th reads the pool thresholds.
	th func(context.Context) notify.Thresholds
}

func (n notifySources) register(svc *notify.Service) {
	svc.AddSource(notify.Source{Scope: notify.Scope{Type: "machine.offline"}, Poll: n.machineOffline})
	svc.AddSource(notify.Source{Scope: notify.Scope{Type: "job.waiting"}, Poll: n.jobWaiting})
	if n.health != nil {
		disks := &diskSource{src: n}
		svc.AddSource(notify.Source{Scope: notify.Scope{Type: "disk.low"}, Poll: disks.poll})
		svc.AddSource(notify.Source{Scope: notify.Scope{Type: "runner.outdated"}, Poll: n.runnerOutdated})
	}
	if n.doctor != nil {
		// A webhook endpoint's own finding (R5.24) is reported by the notifier, not by the doctor.
		svc.AddSource(notify.Source{Scope: notify.Scope{Type: "doctor.finding", ExceptKinds: []string{"endpoint", "notifier"}}, Poll: n.doctorFindings})
	}
	if n.updates != nil {
		svc.AddSource(notify.Source{Scope: notify.Scope{Type: "update.available"}, Poll: n.updateAvailable})
	}
}

func (n notifySources) machineOffline(ctx context.Context) ([]notify.Fact, error) {
	views, err := n.machines.List(ctx)
	if err != nil {
		return nil, err
	}
	grace := time.Duration(n.th(ctx).OfflineGraceMinutes) * time.Minute
	var out []notify.Fact
	for _, v := range views {
		m := v.Machine
		if m.Online || !m.RemovedAt.IsZero() || m.LastSeen.IsZero() || m.IsController {
			continue
		}
		out = append(out, notify.Fact{
			SubjectKind: "machine", SubjectID: m.ID, SubjectName: m.Name, Since: m.LastSeen, Grace: grace,
			Summary: fmt.Sprintf("%s stopped answering at %s.", m.Name, m.LastSeen.UTC().Format("15:04 UTC")),
			Link:    "/setup/machines/" + m.ID,
		})
	}
	return out, nil
}

func (n notifySources) jobWaiting(ctx context.Context) ([]notify.Fact, error) {
	jobs, err := n.db.QueuedJobs(ctx)
	if err != nil {
		return nil, err
	}
	wait := time.Duration(n.th(ctx).JobWaitingMinutes) * time.Minute
	now := n.now()
	var out []notify.Fact
	for _, j := range jobs {
		name := j.Repository + ": " + j.Name
		out = append(out, notify.Fact{
			SubjectKind: "job", SubjectID: fmt.Sprint(j.ID), SubjectName: name, Since: j.QueuedAt, Grace: wait,
			Summary: fmt.Sprintf("%s has waited %d minutes for a runner.", name, int(now.Sub(j.QueuedAt).Minutes())),
			Link:    "/pool/jobs",
		})
	}
	return out, nil
}

// diskSource keeps disk.low firing until free space is 2 points above the threshold (R5.1).
type diskSource struct {
	src    notifySources
	firing map[string]bool
}

func (d *diskSource) poll(ctx context.Context) ([]notify.Fact, error) {
	views, err := d.src.health.Views(ctx)
	if err != nil {
		return nil, err
	}
	thr := float64(d.src.th(ctx).DiskPercent)
	if d.firing == nil {
		d.firing = map[string]bool{}
	}
	var out []notify.Fact
	seen := map[string]bool{}
	for _, v := range views {
		for _, dk := range v.Disks {
			if !dk.Known {
				continue
			}
			id := v.MachineID + ":" + dk.Path
			low := dk.FreePct < thr || (d.firing[id] && dk.FreePct < thr+notify.DiskResolveMargin)
			if !low {
				continue
			}
			seen[id] = true
			out = append(out, notify.Fact{
				SubjectKind: "machine", SubjectID: id, SubjectName: v.MachineID + " " + dk.Path,
				Summary: fmt.Sprintf("%s %s has %.0f%% free space left.", v.MachineID, dk.Path, dk.FreePct),
				Link:    "/setup/machines/" + v.MachineID,
			})
		}
	}
	d.firing = seen
	return out, nil
}

func (n notifySources) runnerOutdated(ctx context.Context) ([]notify.Fact, error) {
	views, err := n.health.Views(ctx)
	if err != nil {
		return nil, err
	}
	var out []notify.Fact
	for _, v := range views {
		if !v.RunnerOutOfDate && !v.AgentOutOfDate {
			continue
		}
		what := "The runner app"
		if !v.RunnerOutOfDate {
			what = "The agent"
		}
		out = append(out, notify.Fact{
			SubjectKind: "machine", SubjectID: v.MachineID, SubjectName: v.MachineID,
			Summary: fmt.Sprintf("%s on %s is older than the version the pool pins.", what, v.MachineID),
			Link:    "/setup/machines/" + v.MachineID,
		})
	}
	return out, nil
}

func (n notifySources) doctorFindings(ctx context.Context) ([]notify.Fact, error) {
	list, err := n.doctor.Findings(ctx, "", "")
	if err != nil {
		return nil, err
	}
	var out []notify.Fact
	for _, f := range list.Items {
		var sev string
		switch f.Severity {
		case gen.SeverityFail:
			sev = "error"
		case gen.SeverityWarn:
			sev = "warning"
		case gen.SeverityInfo:
			sev = "info"
		default:
			continue // ok and skip are not findings
		}
		kind := f.Subject.Kind
		if kind == "" {
			kind = "pool"
		}
		out = append(out, notify.Fact{
			SubjectKind: kind, SubjectID: f.Check + ":" + f.Subject.Id, SubjectName: strings.TrimSpace(f.Title), Severity: sev,
			Summary: strings.TrimSpace(f.Title), Link: "/doctor",
		})
	}
	return out, nil
}

func (n notifySources) updateAvailable(ctx context.Context) ([]notify.Fact, error) {
	st := n.updates.Status(ctx)
	if !st.Available || st.Latest == "" {
		return nil, nil
	}
	return []notify.Fact{{
		SubjectKind: "pool", SubjectID: "crsp", SubjectName: "crsp " + st.Latest,
		Summary: fmt.Sprintf("crsp %s is out. This pool runs %s.", st.Latest, st.Current), Link: "/settings/updates",
	}}, nil
}
