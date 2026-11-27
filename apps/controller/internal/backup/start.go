package backup

import (
	"context"
	"log/slog"
	"net/http"
	"sync"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/settings"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store"
)

// StartOptions are what the controller gives the operations area.
type StartOptions struct {
	DB         *store.Store
	DataDir    string
	ConfigFile string
	Version    string
	Logger     *slog.Logger
	// Restart asks the process to exit so the service manager starts it again
	// after a restore has been staged.
	Restart func()
	// DrillHandler builds the controller's HTTP handler over a database, for the
	// restore drill. It must make no GitHub call.
	DrillHandler func(db *store.Store) http.Handler
}

// Ops is the operations area, ready to be mounted on the API.
type Ops struct {
	Settings *settings.Service
	Audit    *audit.Log
	Backup   *Service

	wg sync.WaitGroup
}

// Wait blocks until the background jobs started by Start have returned. Cancel
// the context given to Start first; callers that remove the data directory
// afterwards (tests) call Wait so nothing writes into it any more.
func (o *Ops) Wait() { o.wg.Wait() }

// Start builds the settings, audit and backup services and starts the daily
// backup and the hourly retention job. They stop when ctx ends. Call
// ApplyPending before the database is opened, then Start.
func Start(ctx context.Context, o StartOptions) *Ops {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	set := settings.New(o.DB)
	log := audit.New(o.DB)
	svc := New(Options{
		DB: o.DB, Settings: set, Audit: log, DataDir: o.DataDir, ConfigFile: o.ConfigFile, Version: o.Version,
		Logger: o.Logger, Restart: o.Restart, DrillHandler: o.DrillHandler,
	})
	ret := &settings.Retention{DB: o.DB, Settings: set, Audit: log, DataDir: o.DataDir, Logger: o.Logger}
	ops := &Ops{Settings: set, Audit: log, Backup: svc}
	ops.wg.Add(2)
	go func() { defer ops.wg.Done(); svc.Run(ctx) }()
	go func() { defer ops.wg.Done(); ret.Run(ctx) }()
	return ops
}
