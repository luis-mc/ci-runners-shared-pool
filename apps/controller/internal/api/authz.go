package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
)

// Authenticator says who a request acts as: a session cookie or a CLI bearer
// token. GitHubArea is the production one.
type Authenticator interface {
	Authenticate(ctx context.Context, r *http.Request) (auth.Principal, error)
}

// otherAreaAuth is the role of every operation outside the GitHub area
// (githubAreaAuth holds those), copied from x-auth in
// packages/contract/openapi.yaml. TestEveryOperationHasItsRole keeps the two
// tables together equal to the contract, operation by operation (web-console
// R6.5). An operation that is in neither table is refused: the check fails
// closed.
var otherAreaAuth = map[string]string{
	// CI run insights (CIX8 contract, docs/features/ci-run-insights R5.42).
	"listInsightFindings": authUser, "getInsightFinding": authUser, "getInsightPlan": authUser, "getInsightSummary": authUser,
	"getWaitSplit": authUser, "getPoolTimeline": authUser, "getPoolSizing": authUser, "listTestInsights": authUser,
	"listGuardrailStatus": authUser, "listInsightDigests": authUser, "getRunSavings": authUser, "listWorkflowChecks": authUser,
	"getJobResources": authUser, "createInsightPlan": authAdmin, "startInsightPlan": authAdmin, "dismissInsightFinding": authAdmin,
	// The pool image registry (REG10 contract, docs/features/registry).
	"getRegistryOverview": authUser, "listRegistryImages": authUser, "getRegistryImage": authUser, "compareRegistryImage": authUser,
	"getRegistryBudgets": authUser, "getRegistryStoreSettings": authUser, "getRegistryReplacePlan": authUser,
	"getRegistryMirrorSettings": authUser, "listImageBuilds": authUser, "getImageBuild": authUser, "listRegistryPulls": authUser,
	"deleteRegistryImage": authAdmin, "putRegistryBudgets": authAdmin, "putRegistryStoreSettings": authAdmin,
	"startRegistryCleanup": authAdmin, "createRegistryReplacePlan": authAdmin, "startRegistryReplacePlan": authAdmin,
	"stopRegistryReplacePlan": authAdmin, "rollbackRegistryReplacePlan": authAdmin, "putRegistryMirrorSettings": authAdmin,

	// crsp's job and run timers (OPS81, docs/features/scheduling R5.99 to R5.106).
	"reregisterRunner": authAdmin, "getListenerGate": authUser, "putListenerGate": authAdmin, "getTimerSettings": authUser, "checkTimerSettings": authUser, "getJobTimer": authUser, "getRunTimer": authUser,
	"putTimerSettings": authAdmin, "stopJobNow": authAdmin, "extendJobTimer": authAdmin, "cancelRunNow": authAdmin, "extendRunTimer": authAdmin,

	// Debug access to a running job (CRSP331, docs/features/debug-access R5.2, R5.26, R5.30). The service checks
	// the role again: Owner only narrowing, the user's own session, the kill switch for the owner.
	"getDebugPoolSettings": authUser, "putDebugPoolSettings": authOwner, "getRepoDebugAccess": authUser, "setRepoDebugAccess": authAdmin,
	"getJobDebug": authUser, "openDebugSession": authAdmin, "listDebugSessions": authUser, "getDebugSession": authUser,
	"extendDebugSession": authAdmin, "endDebugSession": authAdmin, "getDebugTranscript": authAdmin,

	"getCaCertificate": authPublic, "getPackage": authPublic, "listAgentInstallers": authUser, "importAgentInstaller": authAdmin, "getHealth": authPublic,

	"createConsoleLink": authUser, "exportAuditEvents": authUser, "exportJobs": authUser, "getImage": authUser, "getSpeedComparison": authUser, "getSavingsReport": authUser, "getSavingsRates": authUser, "updateSavingsRates": authOwner, "resetSavingsRates": authOwner,
	"getUpdateStatus": authUser, "getLifecycleStatus": authUser, "listUpdateHistory": authUser, "listUpdateFleet": authUser, "listCaches": authUser, "listImages": authUser, "getDoctorRun": authUser, "getJob": authUser, "getMachine": authUser,
	"getMachineChange": authUser, "getMachineLoad": authUser, "getOverview": authUser, "getPoolSettings": authUser,
	"getQueueSettings": authUser, "getRunner": authUser, "getRunnerEnv": authUser, "listEnvSets": authUser, "listJobSizes": authUser, "listSizeLabels": authUser, "getRunnerClasses": authUser, "getRunnerPlan": authUser, "getSettings": authUser,
	"listAuditEvents": authUser, "listDoctorFindings": authUser, "listJobs": authUser, "listMachines": authUser,
	"listRunners": authUser, "listSlots": authUser, "listAssignLeases": authUser, "streamEvents": authUser,
	"listRuns": authUser, "getRun": authUser, "getCommitView": authUser, "listJobGroups": authUser, "getJobStats": authUser, "listJobSummaries": authUser, "getForecast": authUser,
	"exportJobSummaries": authUser, "getCacheHub": authUser,
	"putCacheSettings": authAdmin,

	"applyUpdate": authAdmin, "setUpdatePolicy": authAdmin, "installUpdate": authAdmin, "checkForUpdate": authAdmin, "exportConfig": authAdmin, "rebuildImage": authAdmin,
	"restartService": authAdmin, "getMetricsSettings": authAdmin, "putMetricsSettings": authAdmin, "makeMetricsToken": authAdmin, "getListenAddress": authAdmin, "putListenAddress": authAdmin, "stopRunnerPlan": authAdmin, "updateImageLayers": authAdmin, "wipeCache": authAdmin,
	"createJoinCode": authAdmin, "createJoinFile": authAdmin, "joinMachineOverSsh": authAdmin, "createRunners": authAdmin, "decideMachineChange": authAdmin, "deleteRunner": authAdmin, "deleteRunnerRegistration": authAdmin, "updateRunnerSettings": authAdmin, "clearRunnerCache": authAdmin,
	"drainMachine": authAdmin, "freeStaleSlots": authAdmin, "getInstallCommand": authAdmin, "getJoinCode": authAdmin,
	"listBackups": authAdmin, "searchLogs": authAdmin, "planRunnerReplacement": authAdmin, "planRunners": authAdmin, "redetectMachine": authAdmin, "recoverMachine": authAdmin, "checkMachinePoolSize": authAdmin,
	"removeMachine": authAdmin, "resumeMachine": authAdmin, "restartMachineService": authAdmin, "revokeJoinCode": authAdmin, "runBackup": authAdmin,
	"runBackupDrill": authAdmin, "runDoctor": authAdmin, "updateMachine": authAdmin, "updatePoolSettings": authAdmin,
	"updateQueueSettings": authAdmin, "putEnvSet": authAdmin, "putJobSizeOverride": authAdmin, "putRunnerClasses": authAdmin, "deleteJobSizeOverride": authAdmin, "deleteEnvSet": authAdmin, "getRegistrySettings": authAdmin,
	"putRegistryLogin": authAdmin, "deleteRegistryLogin": authAdmin, "putRegistryCaBundle": authAdmin, "deleteRegistryCaBundle": authAdmin,

	"exportConfigFile": authAdmin, "planConfig": authAdmin, "applyConfigPlan": authOwner,
	"checkMachineDependencies": authAdmin, "startMachineDependencyJob": authAdmin, "stopMachineDependencyJob": authAdmin, "undoMachineDependencyJob": authAdmin, "restartMachineForDependencyJob": authAdmin,
	"listMachineDependencyJobs": authUser, "getMachineDependencyJob": authUser,

	"restoreBackup": authOwner, "removeCrsp": authOwner, "runMachineCommand": authOwner, "removeRunnerLeftovers": authAdmin, "removeRegistration": authAdmin, "startDockerCompact": authAdmin, "updateSettings": authOwner,
}

// roleFor returns the role an operation needs (public, user, admin or owner).
func roleFor(op string) (string, bool) {
	if r, ok := githubAreaAuth[op]; ok {
		return r, true
	}
	r, ok := otherAreaAuth[op]
	return r, ok
}

// roleAllows reports whether p may call an operation that needs `need`.
func roleAllows(p auth.Principal, need string) bool {
	switch need {
	case authUser:
		return p.Role.AtLeast(auth.RoleViewer)
	case authAdmin:
		return p.Role.AtLeast(auth.RoleAdmin)
	case authOwner:
		return p.Role.AtLeast(auth.RoleOwner)
	}
	return false
}

type authz struct {
	authn  Authenticator
	github bool
	log    *slog.Logger
}

func newAuthz(authn Authenticator, github bool, log *slog.Logger) *authz {
	if log == nil {
		log = slog.Default()
	}
	return &authz{authn: authn, github: github, log: log}
}

// middleware is the one gate in front of every operation. Order: the GitHub
// area must be wired for its operations; a public operation runs; any other
// needs a signed-in principal whose role meets x-auth; a change made with a
// browser session also needs the console's own Origin and the session's CSRF
// token. The server decides, whatever the console shows (R6.2, R6.5).
func (z *authz) middleware() gen.StrictMiddlewareFunc {
	return func(next gen.StrictHandlerFunc, operationID string) gen.StrictHandlerFunc {
		op := opID(operationID)
		need, known := roleFor(op)
		_, githubOp := githubAreaAuth[op]
		return func(ctx context.Context, w http.ResponseWriter, r *http.Request, req any) (any, error) {
			ctx = context.WithValue(ctx, keyRequest, r)
			// Role checks come first, so a refused request never learns whether
			// the area behind the operation is built.
			run := func() (any, error) {
				if githubOp && !z.github {
					WriteError(w, http.StatusNotImplemented, CodeNotImplemented, "the GitHub integration is not wired in this build")
					return nil, nil
				}
				return next(ctx, w, r, req)
			}
			var p auth.Principal
			err := auth.ErrUnauthenticated
			if z.authn != nil {
				p, err = z.authn.Authenticate(ctx, r)
			}
			if err == nil {
				ctx = withPrincipal(ctx, p)
			}
			if known && need == authPublic {
				// A browser change must come from the console's own page even
				// where nobody is signed in yet (the setup form).
				if unsafeMethod(r.Method) && !originAllowed(r, false) {
					WriteError(w, http.StatusForbidden, CodeForbidden, "the request does not come from this console")
					return nil, nil
				}
				return run()
			}
			if !known {
				z.log.Error("operation has no role rule; refused", "operation", operationID)
				WriteError(w, http.StatusForbidden, CodeForbidden, "your role does not allow this")
				return nil, nil
			}
			if err != nil {
				if errors.Is(err, auth.ErrUnauthenticated) {
					WriteError(w, http.StatusUnauthorized, CodeUnauthenticated, "sign in first")
				} else {
					status, code, msg := errorFor(err)
					if status == http.StatusInternalServerError {
						z.log.Error("authentication failed", "err", err)
					}
					WriteError(w, status, code, msg)
				}
				return nil, nil
			}
			if !roleAllows(p, need) {
				WriteError(w, http.StatusForbidden, CodeForbidden, "your role does not allow this")
				return nil, nil
			}
			if unsafeMethod(r.Method) && p.SessionHash != "" {
				if !originAllowed(r, true) {
					WriteError(w, http.StatusForbidden, CodeForbidden, "the request does not come from this console")
					return nil, nil
				}
				if !auth.CheckCSRF(p, r.Header.Get("X-CSRF-Token")) {
					WriteError(w, http.StatusForbidden, CodeForbidden, "the CSRF token is missing or wrong")
					return nil, nil
				}
			}
			return run()
		}
	}
}

// originAllowed checks the Origin header of a change against the address the
// browser used. With a session (required) the header must be there and equal
// the console's own origin; without one, a header that is there must match and
// a missing one is a non-browser client (R6.2).
func originAllowed(r *http.Request, required bool) bool {
	got := r.Header.Get("Origin")
	if got == "" {
		return !required
	}
	return strings.EqualFold(got, origin(r))
}

// withPrincipal puts the signed-in principal in ctx and names it as the actor
// of every area that audits a change. The areas read the actor through three
// keys (audit, machines, runners); setting only the principal left them
// recording "unknown" or "system" for a change made in the console.
func withPrincipal(ctx context.Context, p auth.Principal) context.Context {
	ctx = context.WithValue(ctx, keyPrincipal, p)
	if p.Login == "" {
		return ctx
	}
	id := ""
	if p.UserID != 0 {
		id = strconv.FormatInt(p.UserID, 10)
	}
	// An actor a caller already named on ctx stays; only a missing one is filled in.
	if audit.ActorFrom(ctx) == audit.System {
		ctx = audit.WithActor(ctx, audit.Actor{Login: p.Login, ID: id, Source: p.Source})
	}
	if machines.ActorFrom(ctx) == "unknown" {
		ctx = machines.WithActor(ctx, p.Login)
	}
	if actorFrom(ctx) == "unknown" {
		ctx = ContextWithActor(ctx, p.Login)
	}
	return ctx
}
