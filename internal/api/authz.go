package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/internal/machines"
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

	"getCaCertificate": authPublic, "getPackage": authPublic, "getHealth": authPublic,

	"createConsoleLink": authUser, "exportAuditEvents": authUser, "exportJobs": authUser, "getImage": authUser, "getSpeedComparison": authUser,
	"getUpdateStatus": authUser, "listCaches": authUser, "listImages": authUser, "getDoctorRun": authUser, "getJob": authUser, "getMachine": authUser,
	"getMachineChange": authUser, "getMachineLoad": authUser, "getOverview": authUser, "getPoolSettings": authUser,
	"getQueueSettings": authUser, "getRunner": authUser, "getRunnerEnv": authUser, "listEnvSets": authUser, "getRunnerPlan": authUser, "getSettings": authUser,
	"listAuditEvents": authUser, "listDoctorFindings": authUser, "listJobs": authUser, "listMachines": authUser,
	"listRunners": authUser, "listSlots": authUser, "streamEvents": authUser,
	"listRuns": authUser, "getRun": authUser, "listJobGroups": authUser, "getJobStats": authUser, "listJobSummaries": authUser, "getForecast": authUser,
	"exportJobSummaries": authUser,

	"applyUpdate": authAdmin, "checkForUpdate": authAdmin, "exportConfig": authAdmin, "rebuildImage": authAdmin,
	"restartService": authAdmin, "getListenAddress": authAdmin, "putListenAddress": authAdmin, "stopRunnerPlan": authAdmin, "updateImageLayers": authAdmin, "wipeCache": authAdmin,
	"createJoinCode": authAdmin, "createJoinFile": authAdmin, "joinMachineOverSsh": authAdmin, "createRunners": authAdmin, "decideMachineChange": authAdmin, "deleteRunner": authAdmin, "deleteRunnerRegistration": authAdmin, "updateRunnerSettings": authAdmin, "clearRunnerCache": authAdmin,
	"drainMachine": authAdmin, "freeStaleSlots": authAdmin, "getInstallCommand": authAdmin, "getJoinCode": authAdmin,
	"listBackups": authAdmin, "planRunnerReplacement": authAdmin, "planRunners": authAdmin, "redetectMachine": authAdmin, "recoverMachine": authAdmin, "checkMachinePoolSize": authAdmin,
	"removeMachine": authAdmin, "resumeMachine": authAdmin, "revokeJoinCode": authAdmin, "runBackup": authAdmin,
	"runBackupDrill": authAdmin, "runDoctor": authAdmin, "updateMachine": authAdmin, "updatePoolSettings": authAdmin,
	"updateQueueSettings": authAdmin, "putEnvSet": authAdmin, "deleteEnvSet": authAdmin, "getRegistrySettings": authAdmin,
	"putRegistryLogin": authAdmin, "deleteRegistryLogin": authAdmin, "putRegistryCaBundle": authAdmin, "deleteRegistryCaBundle": authAdmin,

	"restoreBackup": authOwner, "runMachineCommand": authOwner, "removeRunnerLeftovers": authAdmin, "removeRegistration": authAdmin, "startDockerCompact": authAdmin, "updateSettings": authOwner,
}

// roleFor returns the role an operation needs (public, user, admin or owner).
func roleFor(op string) (string, bool) {
	if r, ok := githubAreaAuth[op]; ok {
		return r, true
	}
	r, ok := otherAreaAuth[op]
	return r, ok
}

// scopeOps lists, per access scope, the only operations an access credential of
// that scope may call (CRSP134). The scope is the whole authorization: the
// credential has no user and no role beyond it. `parity` is what `crsp parity
// check` calls: the runner list and a doctor run with its result. A doctor run
// writes one history row and changes nothing in the pool.
var scopeOps = map[string]map[string]bool{
	auth.ScopeParity: {"listRunners": true, "runDoctor": true, "getDoctorRun": true},
}

// scopeAllows reports whether an access scope may call an operation. An
// unknown scope allows nothing.
func scopeAllows(scope, op string) bool { return scopeOps[scope][op] }

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
			if p.Scope != "" {
				// An access credential: its scope names the operations, and the
				// role table does not apply (it has no user behind it).
				if !scopeAllows(p.Scope, op) {
					WriteError(w, http.StatusForbidden, CodeForbidden, "this access credential cannot do that")
					return nil, nil
				}
			} else if !roleAllows(p, need) {
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
