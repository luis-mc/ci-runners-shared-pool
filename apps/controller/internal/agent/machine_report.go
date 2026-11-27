package agent

import (
	"context"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/macvm"
	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
)

// MacVMSource reads the macOS VM state of this Mac (macvm.Limiter.Report).
type MacVMSource func(ctx context.Context) (macvm.Report, error)

// addMacVMs sets the macOS VM count of the report. A source that fails is
// reported as not known, never as zero: the controller must not start a VM on
// a count it could not read (machines R5.14).
func addMacVMs(ctx context.Context, rep *agentv1.MachineReport, src MacVMSource) {
	if src == nil {
		return
	}
	r, err := src(ctx)
	rep.MacosVms = &agentv1.MacVmReport{
		Known: err == nil && r.Known, Running: int32(r.Running), Ours: int32(r.Ours), //nolint:gosec // at most a few VMs
		Limit: macvm.MaxVMs, UnusableReason: r.Unusable,
	}
}

// ImageSource lists the job images of this machine; ok is false when the agent
// has no container backend, so the list is not known rather than empty.
type ImageSource func(ctx context.Context) (images []runners.Image, ok bool, err error)

// BuildMachineReport reads the KVM state, the container engine and the images.
// It reads only: it never loads a module, edits `.wslconfig` or starts an
// engine (machines R5.53, runners R5.47). facts carries what `.wslconfig` sets.
func BuildMachineReport(ctx context.Context, sys Sys, facts *agentv1.Facts, imgs ImageSource, now time.Time) *agentv1.MachineReport {
	return buildMachineReport(ctx, sys, facts, imgs, nil, now)
}

func buildMachineReport(ctx context.Context, sys Sys, facts *agentv1.Facts, imgs ImageSource, pkgFails func() []runners.PackageFailure, now time.Time) *agentv1.MachineReport {
	return buildMachineReportWith(ctx, sys, facts, imgs, pkgFails, ProbeSymlink, now)
}

func buildMachineReportWith(ctx context.Context, sys Sys, facts *agentv1.Facts, imgs ImageSource, pkgFails func() []runners.PackageFailure, symlink SymlinkProber, now time.Time) *agentv1.MachineReport {
	rep := &agentv1.MachineReport{ReportedAtUnixMs: now.UnixMilli(), Kvm: &agentv1.KvmReport{}, Registry: true} // Registry: this agent understands the registry calls (R5.19)
	k := rep.Kvm
	if sys.GOOS() == "windows" {
		w := ReadWSLKVM(sys)
		k.DeviceKnown, k.DevicePresent = w.Known, w.DevicePresent
		k.ModuleKnown, k.ModuleLoaded, k.Module = w.Known, w.ModuleLoaded, w.Module
		k.NestedKnown, k.NestedVirtualization = facts.GetWslConfigPresent(), facts.GetWslNestedVirtualization()
	} else {
		k.DeviceKnown, k.DevicePresent = true, sys.ReadWritable("/dev/kvm")
	}
	e := DetectEngine(sys)
	rep.Engine = &agentv1.EngineReport{Kind: string(e.Kind), Usable: e.Usable, Version: e.Version, Reason: e.Reason, ContainerOs: e.ContainerOS}
	if imgs != nil {
		list, ok, err := imgs(ctx)
		if ok && err == nil {
			rep.ImagesKnown = true
			for _, im := range list {
				rep.Images = append(rep.Images, &agentv1.ImageReport{
					Key: im.Key, Tag: im.Tag, Os: im.OS, Layers: im.Layers, SizeMb: im.SizeMB, CreatedUnixMs: im.CreatedAt.UnixMilli(),
				})
			}
		}
	}
	if pkgFails != nil {
		for _, f := range pkgFails() {
			rep.PackageFailures = append(rep.PackageFailures, &agentv1.PackageFailure{
				SetHash: f.SetHash, Names: f.Names, Error: f.Error, FailedUnixMs: f.At.UnixMilli()})
		}
	}
	if symlink != nil {
		rep.Symlink = symlink(ctx, sys)
	}
	return rep
}

// reportMachine builds and sends one MachineReport.
func (l *link) reportMachine(ctx context.Context, commandID string) {
	l.factMu.Lock()
	f := l.facts
	l.factMu.Unlock()
	rep := buildMachineReportWith(ctx, l.cfg.sys(), f, l.cfg.Images, l.cfg.PackageFailures, l.cfg.symlinkProber(), time.Now())
	addMacVMs(ctx, rep, l.cfg.MacVMs)
	rep.CommandId = commandID
	rep.RunnerSwap = l.cfg.RunnerFiles != nil
	rep.HostCommand = true
	rep.DependencySetup = true
	rep.RunnerLeftovers = true
	rep.DockerDisk = dockerDiskReport(ctx, l.cfg.dockerHost())
	rep.RunnerServices = healServices(ctx, l.cfg.serviceHealer(), l.cfg.Logger, l.cfg.redactor())
	_ = l.send(&agentv1.SessionRequest{Message: &agentv1.SessionRequest_MachineReport{MachineReport: rep}})
}
