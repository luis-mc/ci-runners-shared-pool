// Package wire builds the live view and the doctor for the controller in one
// call each, so cmd/crsp and the server need only a line to mount them.
package wire

import (
	"context"
	"log/slog"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/doctor/checks"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/events"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/overview"
)

// NewLive makes the event hub and the live service over a source. Producers
// publish to the returned hub; mount the Live in api.Options.Live. The caller
// stops the hub at shutdown.
func NewLive(src overview.Source, opt events.Options) (*events.MemHub, api.Live) {
	svc := &overview.Service{Source: src}
	opt.Snapshotter = svc
	hub := events.NewHub(opt)
	svc.Events = hub
	return hub, api.Live{Overviews: svc, Hub: hub}
}

// DoctorDeps are what the doctor reads and writes.
type DoctorDeps struct {
	Fleet doctor.Fleet
	// GitHub returns the GitHub adapter, or nil when the App is not set up.
	GitHub   func(ctx context.Context) (doctor.GitHub, error)
	Store    doctor.Store
	Events   events.Publisher
	Logger   *slog.Logger
	PoolName string
	// Extra registers the checks of other areas (cache, backend, ...).
	Extra []doctor.Check
	// KVM and Engines are what agents reported; nil makes those checks skip.
	KVM     doctor.KVMSource
	Engines doctor.EngineSource
	// Demand is what the quiet-demand watchdog found; nil makes demand.quiet skip.
	Demand doctor.DemandSource
	// Ephemeral says whether the pool can make an ephemeral runner; nil makes
	// repo.ephemeral_capacity skip.
	Ephemeral doctor.EphemeralSource
	// Timers tells whether the timers enforce; nil makes timers.mode skip.
	Timers doctor.TimerSource
	// ListenerGate tells whether the listener gate enforces; nil makes listener.gate skip.
	ListenerGate doctor.GateSource
	// RunnerPin tells how far the runner pin is behind; nil makes runner.pin_age skip.
	RunnerPin doctor.RunnerPinSource
}

// NewDoctor makes the engine with the M1 core checks and every extra check,
// and the service the API mounts in api.Options.Doctor.
func NewDoctor(d DoctorDeps) (*doctor.Engine, *doctor.Service) {
	reg := doctor.NewRegistry()
	checks.Register(reg)
	for _, c := range d.Extra {
		reg.Register(c)
	}
	eng := &doctor.Engine{
		Registry: reg, Store: d.Store, Events: d.Events, Logger: d.Logger,
		Env: func(ctx context.Context) (doctor.Env, error) {
			env := doctor.Env{Fleet: d.Fleet, PoolName: d.PoolName, KVM: d.KVM, Engines: d.Engines, Demand: d.Demand, Ephemeral: d.Ephemeral, Timers: d.Timers, ListenerGate: d.ListenerGate, RunnerPin: d.RunnerPin}
			if d.GitHub != nil {
				g, err := d.GitHub(ctx)
				if err != nil {
					return doctor.Env{}, err
				}
				env.GitHub = g
			}
			return env, nil
		},
	}
	return eng, &doctor.Service{Engine: eng}
}
