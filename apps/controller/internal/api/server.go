// Package api is the controller's HTTP API. Its routes come from
// packages/contract/openapi.yaml through oapi-codegen (STACKSPEC 4.1); this
// package implements the generated strict server interface.
//
// It reaches the database only through the small interfaces declared here,
// never by importing internal/store (STACKSPEC 3.2, enforced by depguard).
package api

import (
	"context"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/jobsdata"
	"log/slog"
	"net/http"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backup"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/envsets"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/events"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/regcreds"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/scheduler/slots"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/settings"
)

// Pinger is what the health route needs from the database.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Options are the dependencies of the API.
type Options struct {
	Version string
	DB      Pinger
	Logger  *slog.Logger
	// Runners serves the runner operations; nil answers 501 (runners R5.1 to R5.7).
	Runners RunnersService

	// Operations area (CRSP18): settings, audit log, backups. A nil service
	// answers 501 on its routes, like an unbuilt operation.
	Settings *settings.Service
	Audit    *audit.Log
	Backup   *backup.Service
	// Logs searches the runner setup and agent logs (observability R5.21); nil answers 501.
	Logs LogSearcher
	// Config writes the configuration export (exportConfig); nil answers 501.
	Config ConfigExporter
	// ConfigAsCode is the setup file, its plan and its apply (operations R5.28 to R5.30); nil answers 501.
	ConfigAsCode ConfigAsCode
	// Now stamps export file names; nil uses the clock.
	Now func() time.Time
	// Events receives plan.progress after a stop request; nil publishes nothing.
	Events events.Publisher
	// Update serves the update and restart routes (handlers_update.go). Nil answers 501.
	Update UpdateService
	// Lifecycle serves the lifecycle update routes (handlers_lifecycle.go); Fleet lists and asks
	// the other machines; Remove runs a remove. Nil answers 501.
	Lifecycle LifecycleService
	Fleet     FleetService
	Remove    RemoveService
	// GitHub is the GitHub integration area (setup, sign-in, users,
	// repositories). Nil leaves its operations without a backend.
	GitHub *GitHubArea
	// Machines serves the machines routes; nil answers 501 (machines area).
	Machines MachinesService
	// Deps serves the dependency setup routes (handlers_dependencies.go, CRSP248). Nil answers 501.
	Deps DepsService
	// DepStore serves the dependency store routes (handlers_depstore.go, DEP15). Nil answers 501.
	DepStore DepStoreService
	// CA serves GET /api/v1/ca.pem.
	CA CAProvider
	// Packages serves GET /api/v1/packages/{name} (machines R5.65). Nil answers 501.
	Packages PackageServer
	// AgentInstallers lists and imports agent installers (handlers_agentinstallers.go, operations R5.132). Nil answers 501.
	AgentInstallers AgentInstallerService
	// Jobs and Slots serve the jobs and slot routes (handlers_jobs.go).
	Jobs  slots.Jobs
	Slots scheduler.SlotQueue
	// Assign serves the leases of the assign controller (handlers_assign.go).
	Assign AssignLeases
	// JobsData serves the Jobs section: runs, jobs, steps, forecasts, groups, stats and summaries (handlers_jobs.go,
	// handlers_runs.go, handlers_jobstats.go). With it listJobs, getJob and exportJobs read from it, not from Jobs. Nil
	// answers 501 on the new routes.
	JobsData jobsdata.Reader
	// Live serves the overview and the event stream (handlers_live.go). Nil
	// answers 501 like an unbuilt operation.
	Live LiveService
	// Doctor serves the doctor routes (handlers_doctor.go). Nil answers 501.
	Doctor DoctorService
	// RegistryMirror serves the pull-through mirror settings routes (handlers_registry_reg9.go). Nil answers 501.
	RegistryMirror RegistryMirrorService
	// Caches serves the caches routes (handlers_caches.go). Nil answers 501.
	Caches CachesService
	// CacheHub serves the one hub's routes (handlers_caches_hub.go, caches R5.77). Nil answers 501.
	CacheHub CacheHubService
	// Notify serves the notification channel routes (handlers_notify.go). Nil answers 501.
	Notify NotifyService
	// EnvSets serves the environment set routes and the merged runner
	// environment (handlers_envsets.go). Nil answers 501.
	EnvSets *envsets.Service
	// JobSizes serves the job size routes (handlers_jobsizes.go, scheduling R5.89 to R5.97). Nil answers 501.
	JobSizes JobSizeService
	// SizeLabels lists the machines for the size labels screen (scheduling R5.67a).
	SizeLabels SizeLabelMachines
	// RunnerClasses serves the runner size class settings (handlers_runnerclasses.go, scheduling R5.89 to R5.93). Nil answers 501.
	RunnerClasses RunnerClassService
	// Timers serves the timer settings and the grace actions (handlers_timers.go, scheduling R5.99 to R5.106). Nil answers 501.
	Timers TimerService
	// Health serves machine health, job stats and the health thresholds (handlers_machinehealth.go, OPS83). Nil answers 501.
	Health HealthService
	// ListenerGate serves the listener gate setting and its runners (handlers_listenergate.go, runners R5.134). Nil answers 501.
	ListenerGate ListenerGateService
	// PoolName serves the pool name and its rename (handlers_poolname.go, runners R5.178). Nil answers 501.
	PoolName PoolNameService
	// RegistryReplace serves compare, replace plans and rollback of pool registry images
	// (handlers_registry_reg7.go). Nil answers 501.
	RegistryReplace RegistryReplaceService
	// Builds serves the image build records (handlers_registry_img1.go). Nil answers 501.
	Builds BuildsService
	// Registry serves the registry login and CA bundle routes. Nil answers 501.
	Registry *regcreds.Store
	// RegistryRetention serves the registry budgets, store settings, image delete and
	// cleanup routes (handlers_registry_reg6.go). Nil answers 501.
	RegistryRetention RegistryRetentionService
	// RegistryRead serves the pool registry read routes: overview, images, image page, pulls (handlers_registry_reg4.go,
	// registry R5.33 to R5.36). Nil answers 501.
	RegistryRead RegistryReadService
	// Compare serves the speed comparison route (handlers_compare.go). Nil answers 501.
	Compare CompareService
	// Savings and SavingsRates serve the minutes saved report and its rate table (handlers_savings.go). Nil answers 501.
	Savings      SavingsService
	SavingsRates SavingsRates
	// Trial serves the trial mode routes (handlers_trial.go). Nil answers 501.
	Trial TrialService
	// Insights serves the CI run insights routes (handlers_insights.go). Nil answers 501.
	Insights *InsightsArea
	// JobRunners says who ran a job (runner origin, group, labels, steps source, checked at). Nil leaves
	// those fields of a job absent (ci-run-insights R5.39 to R5.41).
	JobRunners JobRunnerReader
	// Listen serves the listen address routes (handlers_listen.go). Nil answers 501.
	Listen ListenService
	// Debug serves debug access to a running job (handlers_debug.go, debug-access R5.1 to R5.30). Nil answers 501.
	Debug DebugService
	// Metrics serves the metrics export settings (handlers_metrics.go, observability R5.40). Nil answers 501.
	Metrics MetricsService
	// Authn says who a request acts as. Nil uses the GitHub area's sessions and
	// CLI tokens; with neither, every operation that needs a role answers 401.
	// A test passes a fixed principal.
	Authn Authenticator
}

type server struct {
	opts Options
	// authn is the authenticator the gate uses; a long-lived stream asks it again (observability R6.4).
	authn Authenticator
}

var _ gen.StrictServerInterface = (*server)(nil)

// Register mounts the API routes on mux. Unknown paths under /api/ answer with
// the JSON error envelope, not the web app.
func Register(mux *http.ServeMux, opts Options) {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	httpMW := []gen.MiddlewareFunc{transportMW}
	authn := opts.Authn
	if opts.GitHub != nil {
		httpMW = append(httpMW, opts.GitHub.HTTPMiddleware())
		if authn == nil {
			authn = opts.GitHub
		}
	}
	// One middleware checks every operation: who signed in, the role of its
	// x-auth, the Origin and the CSRF token of a change (R6.2, R6.5).
	strictMW := []gen.StrictMiddlewareFunc{newAuthz(authn, opts.GitHub != nil, opts.Logger).middleware()}
	strict := gen.NewStrictHandlerWithOptions(&server{opts: opts, authn: authn}, strictMW, gen.StrictHTTPServerOptions{
		RequestErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
			opts.Logger.Debug("bad request", "err", err)
			WriteError(w, http.StatusBadRequest, CodeBadRequest, "the request is not valid")
		},
		ResponseErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
			opts.Logger.Error("handler failed", "err", err)
			WriteError(w, http.StatusInternalServerError, CodeInternal, "internal error")
		},
	})
	gen.HandlerWithOptions(strict, gen.StdHTTPServerOptions{BaseRouter: mux, Middlewares: httpMW})
	mux.HandleFunc("/api/", notFound)
}

// GetHealth implements GET /healthz.
func (s *server) GetHealth(ctx context.Context, _ gen.GetHealthRequestObject) (gen.GetHealthResponseObject, error) {
	if err := s.opts.DB.Ping(ctx); err != nil {
		s.opts.Logger.Error("health: database down", "err", err)
		return gen.GetHealth503JSONResponse{Error: gen.ErrorBody{Code: CodeUnavailable, Message: "database is not available"}}, nil
	}
	return gen.GetHealth200JSONResponse{Status: gen.HealthStatusOk, Version: s.opts.Version}, nil
}

// Recover turns a panic in any handler into a 500 with the error envelope.
func Recover(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v) // a stream that must end the connection (an export that failed half way)
				}
				logger.Error("panic in handler", "panic", v, "path", r.URL.Path)
				WriteError(w, http.StatusInternalServerError, CodeInternal, "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
