package configio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/notify"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/repos"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/secrets"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/settings"
)

// The sections the controller offers. Each wraps the service that already
// owns the data, so every rule (ranges, audit, drain) is the service's own.
// A file never adds a machine or a repository (they arrive by join file and
// by the GitHub App) and never removes one.

// ActorFunc says who is applying; the services audit and authorise by it.
type ActorFunc func(ctx context.Context) (audit.Actor, auth.Principal)

func toFields(v any) (Fields, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	f := Fields{}
	return f, json.Unmarshal(b, &f)
}

func fromFields(f Fields, v any) error {
	b, err := json.Marshal(f)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// changed is the fields of want that differ from old.
func changed(want, old Fields) Fields {
	out := Fields{}
	for k, v := range want {
		ob, _ := json.Marshal(old[k])
		nb, _ := json.Marshal(v)
		if string(ob) != string(nb) || old == nil {
			out[k] = v
		}
	}
	return out
}

func strs(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

// base gives the sections their shared answers.
type base struct {
	name        string
	single      bool
	add, remove bool
}

func (b base) Name() string    { return b.name }
func (b base) Singleton() bool { return b.single }
func (b base) CanAdd() bool    { return b.add }
func (b base) CanRemove() bool { return b.remove }
func (b base) Delete(_ context.Context, name string) error {
	return fmt.Errorf("%s/%s cannot be removed from a file", b.name, name)
}

// ---- settings, pool, queue (singletons) ----

// SettingsService is what the settings section needs.
type SettingsService interface {
	Get(ctx context.Context) (settings.Settings, error)
	Update(ctx context.Context, actor audit.Actor, p settings.Patch) (settings.Settings, error)
	GetPool(ctx context.Context) (settings.PoolSettings, error)
	UpdatePool(ctx context.Context, actor audit.Actor, p settings.PoolSettings) (settings.PoolSettings, error)
	GetQueue(ctx context.Context) (settings.QueueSettings, error)
	UpdateQueue(ctx context.Context, actor audit.Actor, q settings.QueueSettings) (settings.QueueSettings, error)
	GetImageLayers(ctx context.Context, machineID, os string) ([]string, error)
	SetImageLayers(ctx context.Context, actor audit.Actor, machineID, os string, layers []string) ([]string, error)
}

type settingsFields struct {
	RetentionJobHistoryDays    int    `json:"retention_job_history_days"`
	RetentionLogsDays          int    `json:"retention_logs_days"`
	RetentionLogsMiBPerMachine int    `json:"retention_logs_mib_per_machine"`
	RetentionAuditDays         int    `json:"retention_audit_days"`
	RetentionLoadRawHours      int    `json:"retention_load_raw_hours"`
	BackupTimeLocal            string `json:"backup_time_local"`
	BackupKeepDays             int    `json:"backup_keep_days"`
	MaxEventStreams            int    `json:"max_event_streams"`
	FallbackGraceMinutes       int    `json:"fallback_grace_minutes"`
	OverflowLabel              string `json:"overflow_label"`
	AutoUpdate                 bool   `json:"auto_update"`
	InsightsUpdatePeriod       string `json:"insights_update_period"`
	InsightsUpdateEveryNthRuns int    `json:"insights_update_every_nth_runs"`
}

type settingsSection struct {
	base
	svc SettingsService
	who ActorFunc
}

// NewSettings is the section "settings": retention, backup, update and event
// limits.
func NewSettings(svc SettingsService, who ActorFunc) Section {
	return &settingsSection{base{name: "settings", single: true}, svc, who}
}

func (s *settingsSection) Read(ctx context.Context) (map[string]Fields, error) {
	st, err := s.svc.Get(ctx)
	if err != nil {
		return nil, err
	}
	f, err := toFields(settingsFields{
		st.RetentionJobHistoryDays, st.RetentionLogsDays, st.RetentionLogsMiBPerMachine, st.RetentionAuditDays,
		st.RetentionLoadRawHours, st.BackupTimeLocal, st.BackupKeepDays, st.MaxEventStreams, st.FallbackGraceMinutes,
		st.OverflowLabel, st.AutoUpdate, st.InsightsUpdatePeriod, st.InsightsUpdateEveryNthRuns,
	})
	return map[string]Fields{"": f}, err
}

func (s *settingsSection) Write(ctx context.Context, _ string, want, old Fields) error {
	var p struct {
		RetentionJobHistoryDays    *int    `json:"retention_job_history_days"`
		RetentionLogsDays          *int    `json:"retention_logs_days"`
		RetentionLogsMiBPerMachine *int    `json:"retention_logs_mib_per_machine"`
		RetentionAuditDays         *int    `json:"retention_audit_days"`
		RetentionLoadRawHours      *int    `json:"retention_load_raw_hours"`
		BackupTimeLocal            *string `json:"backup_time_local"`
		BackupKeepDays             *int    `json:"backup_keep_days"`
		MaxEventStreams            *int    `json:"max_event_streams"`
		FallbackGraceMinutes       *int    `json:"fallback_grace_minutes"`
		OverflowLabel              *string `json:"overflow_label"`
		AutoUpdate                 *bool   `json:"auto_update"`
		InsightsUpdatePeriod       *string `json:"insights_update_period"`
		InsightsUpdateEveryNthRuns *int    `json:"insights_update_every_nth_runs"`
	}
	if err := fromFields(changed(want, old), &p); err != nil {
		return err
	}
	actor, _ := s.who(ctx)
	period := (*string)(nil)
	if p.InsightsUpdatePeriod != nil {
		period = p.InsightsUpdatePeriod
	}
	_, err := s.svc.Update(ctx, actor, settings.Patch{
		RetentionJobHistoryDays: p.RetentionJobHistoryDays, RetentionLogsDays: p.RetentionLogsDays,
		RetentionLogsMiBPerMachine: p.RetentionLogsMiBPerMachine, RetentionAuditDays: p.RetentionAuditDays,
		RetentionLoadRawHours: p.RetentionLoadRawHours, BackupTimeLocal: p.BackupTimeLocal, BackupKeepDays: p.BackupKeepDays,
		MaxEventStreams: p.MaxEventStreams, FallbackGraceMinutes: p.FallbackGraceMinutes, OverflowLabel: p.OverflowLabel, AutoUpdate: p.AutoUpdate,
		InsightsUpdatePeriod: period, InsightsUpdateEveryNthRuns: p.InsightsUpdateEveryNthRuns,
	})
	return err
}

type poolSection struct {
	base
	svc SettingsService
	who ActorFunc
}

// NewPool is the section "pool": the defaults a machine gets when it joins.
func NewPool(svc SettingsService, who ActorFunc) Section {
	return &poolSection{base{name: "pool", single: true}, svc, who}
}

func (s *poolSection) Read(ctx context.Context) (map[string]Fields, error) {
	p, err := s.svc.GetPool(ctx)
	if err != nil {
		return nil, err
	}
	f, err := toFields(p)
	return map[string]Fields{"": f}, err
}

func (s *poolSection) Write(ctx context.Context, _ string, want, old Fields) error {
	cur, err := s.svc.GetPool(ctx)
	if err != nil {
		return err
	}
	if err := fromFields(changed(want, old), &cur); err != nil {
		return err
	}
	actor, _ := s.who(ctx)
	_, err = s.svc.UpdatePool(ctx, actor, cur)
	return err
}

type queueSection struct {
	base
	svc SettingsService
	who ActorFunc
}

// NewQueue is the section "queue": the order of service.
func NewQueue(svc SettingsService, who ActorFunc) Section {
	return &queueSection{base{name: "queue", single: true}, svc, who}
}

func (s *queueSection) Read(ctx context.Context) (map[string]Fields, error) {
	q, err := s.svc.GetQueue(ctx)
	if err != nil {
		return nil, err
	}
	q.Order = strs(q.Order)
	f, err := toFields(q)
	return map[string]Fields{"": f}, err
}

func (s *queueSection) Write(ctx context.Context, _ string, want, old Fields) error {
	cur, err := s.svc.GetQueue(ctx)
	if err != nil {
		return err
	}
	if err := fromFields(changed(want, old), &cur); err != nil {
		return err
	}
	actor, _ := s.who(ctx)
	_, err = s.svc.UpdateQueue(ctx, actor, cur)
	return err
}

// ---- runner classes ----

// ClassService is the runner size table service.
type ClassService interface {
	Get(ctx context.Context) runners.ClassView
	Save(ctx context.Context, in runners.ClassInput, by string) (runners.ClassView, error)
}

type classSection struct {
	base
	svc ClassService
	who ActorFunc
}

// NewRunnerClasses is the section "runner_classes": the size preset and the
// reserve rule. A hand-made size table is not in the file (preset "custom"
// is kept as it is).
func NewRunnerClasses(svc ClassService, who ActorFunc) Section {
	return &classSection{base{name: "runner_classes", single: true}, svc, who}
}

func (s *classSection) Read(ctx context.Context) (map[string]Fields, error) {
	v := s.svc.Get(ctx)
	f, err := toFields(struct {
		Preset             string `json:"preset"`
		ReserveAfterPasses int    `json:"reserve_after_passes"`
		ReserveAfterMins   int    `json:"reserve_after_minutes"`
	}{v.Preset, v.ReservePasses, v.ReserveMinutes})
	return map[string]Fields{"": f}, err
}

func (s *classSection) Write(ctx context.Context, _ string, want, old Fields) error {
	var p struct {
		Preset             *string `json:"preset"`
		ReserveAfterPasses *int    `json:"reserve_after_passes"`
		ReserveAfterMins   *int    `json:"reserve_after_minutes"`
	}
	if err := fromFields(changed(want, old), &p); err != nil {
		return err
	}
	in := runners.ClassInput{ReservePasses: p.ReserveAfterPasses, ReserveMinutes: p.ReserveAfterMins}
	if p.Preset != nil {
		if *p.Preset == "custom" {
			return fmt.Errorf("a custom size table is set in the console, not from a file")
		}
		in.Preset = *p.Preset
	}
	actor, _ := s.who(ctx)
	_, err := s.svc.Save(ctx, in, actor.Login)
	return err
}

// ---- machines and their image layers ----

// MachineService is what the machine sections need.
type MachineService interface {
	List(ctx context.Context) ([]machines.View, error)
	UpdateSettings(ctx context.Context, actor, id string, u machines.SettingsUpdate) (machines.View, *machines.Change, error)
}

type machineSection struct {
	base
	svc MachineService
	who ActorFunc
}

// NewMachines is the section "machines": the settings of each machine, by
// name. A machine is neither added nor removed from a file.
func NewMachines(svc MachineService, who ActorFunc) Section {
	return &machineSection{base{name: "machines"}, svc, who}
}

func (s *machineSection) Read(ctx context.Context) (map[string]Fields, error) {
	vs, err := s.svc.List(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]Fields{}
	for _, v := range vs {
		st := v.Settings
		mode := st.Mode
		if mode == "" {
			mode = "automatic"
		}
		cap := st.CPUCapPct
		if cap == 0 {
			cap = 100
		}
		out[v.Machine.Name] = Fields{
			"budget_memory_pct": st.BudgetMemoryPct, "budget_cpu_pct": st.BudgetCPUPct, "job_slots": st.JobSlots,
			"light_slots": st.LightSlots, "heavy_slots": st.HeavySlots, "light_jobs": strs(st.LightJobs), "heavy_jobs": strs(st.HeavyJobs),
			"gate_on_timeout": st.GateOnTimeout, "gate_max_wait_minutes": st.GateMaxWaitMinutes,
			"allowed_backends": strs(st.AllowedBackends), "cache_cap_bytes": st.CacheCapBytes,
			"drain_timeout_minutes": st.DrainTimeoutMinutes, "windows_symlink_right": st.WindowsSymlinkRight,
			"pool_memory_gib": st.PoolMemoryGiB, "pool_cpus": st.PoolCPUs, "mode": mode,
			"pause_on_battery": st.PauseOnBattery, "cpu_cap_pct": cap, "disk_floor_gib": st.DiskFloorGiB,
		}
	}
	return out, nil
}

func (s *machineSection) Write(ctx context.Context, name string, want, old Fields) error {
	var p struct {
		BudgetMemoryPct     *int      `json:"budget_memory_pct"`
		BudgetCPUPct        *int      `json:"budget_cpu_pct"`
		JobSlots            *int      `json:"job_slots"`
		LightSlots          *int      `json:"light_slots"`
		HeavySlots          *int      `json:"heavy_slots"`
		LightJobs           *[]string `json:"light_jobs"`
		HeavyJobs           *[]string `json:"heavy_jobs"`
		GateOnTimeout       *string   `json:"gate_on_timeout"`
		GateMaxWaitMinutes  *int      `json:"gate_max_wait_minutes"`
		AllowedBackends     *[]string `json:"allowed_backends"`
		CacheCapBytes       *int64    `json:"cache_cap_bytes"`
		DrainTimeoutMinutes *int      `json:"drain_timeout_minutes"`
		WindowsSymlinkRight *bool     `json:"windows_symlink_right"`
		PoolMemoryGiB       *float64  `json:"pool_memory_gib"`
		PoolCPUs            *int      `json:"pool_cpus"`
		Mode                *string   `json:"mode"`
		PauseOnBattery      *bool     `json:"pause_on_battery"`
		CPUCapPct           *int      `json:"cpu_cap_pct"`
		DiskFloorGiB        *int      `json:"disk_floor_gib"`
	}
	if err := fromFields(changed(want, old), &p); err != nil {
		return err
	}
	id, err := s.idOf(ctx, name)
	if err != nil {
		return err
	}
	actor, _ := s.who(ctx)
	// The machine service drains first when a change restarts runners
	// (operations R5.14); it answers with the pending change.
	_, _, err = s.svc.UpdateSettings(ctx, actor.Login, id, machines.SettingsUpdate{
		BudgetMemoryPct: p.BudgetMemoryPct, BudgetCPUPct: p.BudgetCPUPct, JobSlots: p.JobSlots, LightSlots: p.LightSlots,
		HeavySlots: p.HeavySlots, LightJobs: p.LightJobs, HeavyJobs: p.HeavyJobs, GateOnTimeout: p.GateOnTimeout,
		GateMaxWaitMinutes: p.GateMaxWaitMinutes, AllowedBackends: p.AllowedBackends, CacheCapBytes: p.CacheCapBytes,
		DrainTimeoutMinutes: p.DrainTimeoutMinutes, WindowsSymlinkRight: p.WindowsSymlinkRight, PoolMemoryGiB: p.PoolMemoryGiB,
		PoolCPUs: p.PoolCPUs, Mode: p.Mode, PauseOnBattery: p.PauseOnBattery, CPUCapPct: p.CPUCapPct, DiskFloorGiB: p.DiskFloorGiB,
	})
	return err
}

func (s *machineSection) idOf(ctx context.Context, name string) (string, error) {
	vs, err := s.svc.List(ctx)
	if err != nil {
		return "", err
	}
	for _, v := range vs {
		if v.Machine.Name == name {
			return v.Machine.ID, nil
		}
	}
	return "", fmt.Errorf("no machine named %s", name)
}

type layerSection struct {
	base
	machines MachineService
	svc      SettingsService
	who      ActorFunc
}

// NewImageLayers is the section "image_layers": the layers chosen for the
// image of each machine, by machine name.
func NewImageLayers(m MachineService, svc SettingsService, who ActorFunc) Section {
	return &layerSection{base{name: "image_layers"}, m, svc, who}
}

func (s *layerSection) Read(ctx context.Context) (map[string]Fields, error) {
	vs, err := s.machines.List(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]Fields{}
	for _, v := range vs {
		l, err := s.svc.GetImageLayers(ctx, v.Machine.ID, v.Machine.OS)
		if err != nil {
			return nil, err
		}
		out[v.Machine.Name] = Fields{"layers": strs(l)}
	}
	return out, nil
}

func (s *layerSection) Write(ctx context.Context, name string, want, _ Fields) error {
	var p struct {
		Layers []string `json:"layers"`
	}
	if err := fromFields(want, &p); err != nil {
		return err
	}
	vs, err := s.machines.List(ctx)
	if err != nil {
		return err
	}
	actor, _ := s.who(ctx)
	for _, v := range vs {
		if v.Machine.Name == name {
			_, err := s.svc.SetImageLayers(ctx, actor, v.Machine.ID, v.Machine.OS, p.Layers)
			return err
		}
	}
	return fmt.Errorf("no machine named %s", name)
}

// ---- repository policies ----

// RepoService is what the repository section needs.
type RepoService interface {
	List(ctx context.Context, p auth.Principal, f repos.ListFilter) ([]repos.RepositoryView, error)
	UpdatePolicy(ctx context.Context, p auth.Principal, id int64, u repos.PolicyUpdate) (repos.Policy, error)
}

type repoSection struct {
	base
	svc RepoService
	who ActorFunc
}

// NewRepositories is the section "repositories": the policy of each
// repository, by full name. The pool switch and the fork and hosted-fallback
// yes stay in the console: a change that needs one of those yes is refused
// with the service's own words.
func NewRepositories(svc RepoService, who ActorFunc) Section {
	return &repoSection{base{name: "repositories"}, svc, who}
}

func (s *repoSection) all(ctx context.Context) ([]repos.RepositoryView, error) {
	_, p := s.who(ctx)
	var out []repos.RepositoryView
	after := ""
	for {
		page, err := s.svc.List(ctx, p, repos.ListFilter{AfterName: after, Limit: 200})
		if err != nil {
			return nil, err
		}
		out = append(out, page...)
		if len(page) < 200 {
			return out, nil
		}
		after = page[len(page)-1].FullName
	}
}

func (s *repoSection) Read(ctx context.Context) (map[string]Fields, error) {
	vs, err := s.all(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]Fields{}
	for _, v := range vs {
		p := v.Policy
		kinds := p.RunnerKinds
		if kinds == "" {
			kinds = repos.RunnerKindsDefault
		}
		hosted := map[string]string{}
		for k, val := range p.HostedLabels {
			hosted[k] = val
		}
		out[v.FullName] = Fields{
			"priority": p.Priority, "max_parallel_jobs": p.MaxParallelJobs, "allow_fork_prs": p.AllowForkPRs,
			"labels": strs(p.Labels), "runner_groups": strs(p.RunnerGroups), "budget_memory_pct": p.BudgetMemoryPct,
			"budget_cpu_pct": p.BudgetCPUPct, "extra_packages": strs(p.ExtraPackages), "hosted_fallback": p.HostedFallback,
			"fallback_cap_minutes": p.FallbackCapMinutes, "hosted_labels": hosted, "runner_kinds": kinds,
		}
	}
	return out, nil
}

func (s *repoSection) Write(ctx context.Context, name string, want, old Fields) error {
	var p struct {
		Priority           *int               `json:"priority"`
		MaxParallelJobs    *int               `json:"max_parallel_jobs"`
		AllowForkPRs       *bool              `json:"allow_fork_prs"`
		Labels             *[]string          `json:"labels"`
		RunnerGroups       *[]string          `json:"runner_groups"`
		BudgetMemoryPct    *int               `json:"budget_memory_pct"`
		BudgetCPUPct       *int               `json:"budget_cpu_pct"`
		ExtraPackages      *[]string          `json:"extra_packages"`
		HostedFallback     *bool              `json:"hosted_fallback"`
		FallbackCapMinutes *int               `json:"fallback_cap_minutes"`
		HostedLabels       *map[string]string `json:"hosted_labels"`
		RunnerKinds        *string            `json:"runner_kinds"`
	}
	if err := fromFields(changed(want, old), &p); err != nil {
		return err
	}
	vs, err := s.all(ctx)
	if err != nil {
		return err
	}
	var id int64
	for _, v := range vs {
		if v.FullName == name {
			id = v.ID
		}
	}
	if id == 0 {
		return fmt.Errorf("no repository named %s", name)
	}
	u := repos.PolicyUpdate{
		Priority: p.Priority, MaxParallelJobs: p.MaxParallelJobs, AllowForkPRs: p.AllowForkPRs, Labels: p.Labels,
		RunnerGroups: p.RunnerGroups, BudgetMemoryPct: p.BudgetMemoryPct, BudgetCPUPct: p.BudgetCPUPct,
		HostedFallback: p.HostedFallback, FallbackCapMinutes: p.FallbackCapMinutes, HostedLabels: p.HostedLabels, RunnerKinds: p.RunnerKinds,
	}
	if p.ExtraPackages != nil {
		text := strings.Join(*p.ExtraPackages, "\n")
		u.ExtraPackages = &text
	}
	_, pr := s.who(ctx)
	_, err = s.svc.UpdatePolicy(ctx, pr, id, u)
	return err
}

// ---- notification channels ----

// NotifyService is what the notification section needs.
type NotifyService interface {
	List(ctx context.Context, who notify.Caller) ([]notify.Channel, error)
	Update(ctx context.Context, who notify.Caller, id string, pt notify.Patch) (notify.Channel, error)
}

type notifySection struct {
	base
	svc NotifyService
	who ActorFunc
}

// NewNotifications is the section "notifications": the channels, by name. A
// channel holds a secret (a signing secret, a mail password) that never goes
// in a file, so a file changes a channel's switches but cannot add one.
func NewNotifications(svc NotifyService, who ActorFunc) Section {
	return &notifySection{base{name: "notifications"}, svc, who}
}

func (s *notifySection) caller(ctx context.Context) notify.Caller {
	a, p := s.who(ctx)
	return notify.Caller{Login: p.Login, Admin: true, Actor: a}
}

func (s *notifySection) channels(ctx context.Context) (map[string]notify.Channel, error) {
	cs, err := s.svc.List(ctx, s.caller(ctx))
	if err != nil {
		return nil, err
	}
	sort.Slice(cs, func(i, j int) bool { return cs[i].ID < cs[j].ID })
	out := map[string]notify.Channel{}
	for _, c := range cs {
		key := c.Name
		if _, dup := out[key]; dup {
			key = c.Name + " (" + c.ID + ")"
		}
		out[key] = c
	}
	return out, nil
}

func (s *notifySection) Read(ctx context.Context) (map[string]Fields, error) {
	cs, err := s.channels(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]Fields{}
	for k, c := range cs {
		out[k] = Fields{
			"kind": c.Kind, "enabled": c.Enabled, "events": strs(c.Events), "min_severity": c.MinSeverity,
			"address": c.Address, "url": c.URL,
		}
	}
	return out, nil
}

func (s *notifySection) Write(ctx context.Context, name string, want, old Fields) error {
	var p struct {
		Enabled     *bool     `json:"enabled"`
		Events      *[]string `json:"events"`
		MinSeverity *string   `json:"min_severity"`
		Address     *string   `json:"address"`
		URL         *string   `json:"url"`
	}
	if k, ok := changed(want, old)["kind"]; ok {
		return fmt.Errorf("the kind of a channel cannot change (%v)", k)
	}
	if err := fromFields(changed(want, old), &p); err != nil {
		return err
	}
	cs, err := s.channels(ctx)
	if err != nil {
		return err
	}
	c, ok := cs[name]
	if !ok {
		return fmt.Errorf("no channel named %s", name)
	}
	_, err = s.svc.Update(ctx, s.caller(ctx), c.ID, notify.Patch{
		Enabled: p.Enabled, Events: p.Events, MinSeverity: p.MinSeverity, Address: p.Address, URL: p.URL,
	})
	return err
}

// NamedSecret is a secret the file names by reference: a plain name and its
// key in the OS secret store.
type NamedSecret struct {
	Name string
	Key  secrets.Key
}

// SecretLister returns the references block of an export. It asks the store
// only whether a secret exists and drops the value at once; the value never
// reaches the file.
func SecretLister(store secrets.Store, named []NamedSecret) func(ctx context.Context) []SecretRef {
	return func(ctx context.Context) []SecretRef {
		out := make([]SecretRef, 0, len(named))
		for _, n := range named {
			state := "not set"
			if store != nil {
				v, err := store.Get(ctx, n.Key)
				for i := range v {
					v[i] = 0
				}
				if err == nil {
					state = "set"
				} else if !errors.Is(err, secrets.ErrNotFound) {
					state = "unknown"
				}
			}
			out = append(out, SecretRef{Name: n.Name, Ref: string(n.Key), State: state})
		}
		return out
	}
}
