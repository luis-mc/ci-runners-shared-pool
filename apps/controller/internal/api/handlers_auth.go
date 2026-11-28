package api

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/addresses"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/auth"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/repos"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/secrets"
)

// GitHubArea is the GitHub integration's part of the API: setup, sign-in,
// users, installations, repositories (github-integration section 7). Wire it
// through Options.GitHub; it adds an authentication middleware for its own
// operations (R5.12) and the webhook route (R6.5).
type GitHubArea struct {
	Auth  *auth.Service
	Repos *repos.Service
	// Webhook serves POST /webhooks/github with the raw body (R5.36, R6.5).
	// Nil means webhooks are off.
	Webhook http.Handler
	// DeviceCodeURL and VerificationURL are GitHub's device flow addresses for
	// the CLI (R5.14); empty uses github.com.
	WebBase string
	Logger  *slog.Logger
	// Health is the webhook health of each repository (RES14); nil hides it.
	Health WebhookHealth
	// AppHealth is the last answer of the hourly GET /app (R5.6); nil means
	// the App is never reported gone.
	AppHealth AppHealthSource
	// HasMachine reports whether any machine is in the pool, the controller's own
	// included; it makes the first-machine step of the setup done (machines R5.1).
	// Nil leaves the step to do.
	HasMachine func(ctx context.Context) bool
}

// AppHealthSource is what the console reads about the App (R5.6).
type AppHealthSource interface {
	State() github.AppState
}

// Role needed by each operation of this area, from x-auth in openapi.yaml.
// A test keeps this table equal to the file (R5.12). "public" operations
// check their own state, for example the setup cookie.
const (
	authPublic = "public"
	authUser   = "user"
	authAdmin  = "admin"
	authOwner  = "owner"
)

var githubAreaAuth = map[string]string{
	"getSetupState": authPublic, "redeemSetupLink": authPublic, "startAppManifest": authPublic,
	"completeAppManifest": authPublic, "startSignIn": authPublic, "completeSignIn": authPublic, "redeemSignInHandoff": authPublic, "checkSignInAddress": authAdmin,
	"redeemTrayLink": authPublic, "getCliLoginConfig": authPublic, "exchangeCliToken": authPublic,
	"listCliTokens": authUser, "revokeCliToken": authUser, "signOut": authUser, "getSession": authUser,
	"getPreferences": authUser, "updatePreferences": authUser,
	"listUsers": authOwner, "addUser": authOwner, "updateUserRole": authOwner, "removeUser": authOwner,
	"listInstallations": authUser, "listOrgPools": authUser, "setOrgPool": authAdmin, "getAppInstallUrl": authAdmin, "getAppHealth": authUser, "listRepositories": authUser,
	"planRepositoryChanges": authAdmin, "stopRepositoryPlan": authAdmin, "getRepositoryPlan": authUser, "applyRepositoryChanges": authAdmin,
	"getRepository": authUser, "updateRepositoryPolicy": authAdmin, "getRepositoryRunsOn": authUser,
	"previewRunsOnPullRequests": authAdmin, "openRunsOnPullRequests": authAdmin,
	"getRepositoryExtraPackages": authUser, "setRepositoryExtraPackages": authAdmin,
	"getRepositoryTrial": authUser, "setRepositoryTrial": authAdmin, "setRepositoryWebhookMode": authAdmin,
	"getAppWebhook": authAdmin, "setAppWebhook": authOwner, "clearAppWebhook": authOwner,
	"replaceAppPrivateKey": authOwner, "getPublicRepositories": authUser, "setPublicRepositories": authOwner,
	"getGithubGrant":       authAdmin,
	"receiveGithubWebhook": authPublic,
	// CRSP37 notification channels: any signed-in user; a webhook needs an admin (checked in the service).
	"listNotificationChannels": authUser, "createNotificationChannel": authUser, "updateNotificationChannel": authUser,
	"deleteNotificationChannel": authUser, "testNotificationChannel": authUser,
	// CRSP258: the notices list for any signed-in user; the pool thresholds change only by an admin.
	"listNotices": authUser, "getNotificationThresholds": authUser, "setNotificationThresholds": authAdmin,
}

// operationIDs maps the generated handler names to openapi operation ids.
func opID(generated string) string {
	if generated == "" {
		return ""
	}
	return strings.ToLower(generated[:1]) + generated[1:]
}

type ctxKey int

const (
	keyRequest ctxKey = iota + 1
	keyPrincipal
)

// requestFrom returns the HTTP request of a strict handler call.
func requestFrom(ctx context.Context) *http.Request {
	r, _ := ctx.Value(keyRequest).(*http.Request)
	return r
}

// PrincipalFrom returns who the request acts as, if the caller signed in.
// Other areas use it too.
func PrincipalFrom(ctx context.Context) (auth.Principal, bool) {
	p, ok := ctx.Value(keyPrincipal).(auth.Principal)
	return p, ok
}

// HTTPMiddleware routes the webhook with its raw body, before the generated
// handler reads it, so the signature is checked on the exact bytes (R6.5).
func (a *GitHubArea) HTTPMiddleware() gen.MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/webhooks/github" && r.Method == http.MethodPost {
				if a.Webhook == nil {
					WriteError(w, http.StatusNotFound, CodeNotFound, "webhooks are off for this controller")
					return
				}
				a.Webhook.ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func unsafeMethod(m string) bool {
	return m != http.MethodGet && m != http.MethodHead && m != http.MethodOptions
}

// Authenticate implements Authenticator: a CLI bearer token or the session cookie.
func (a *GitHubArea) Authenticate(ctx context.Context, r *http.Request) (auth.Principal, error) {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		tok := strings.TrimPrefix(h, "Bearer ")
		return a.Auth.AuthenticateCLI(ctx, tok)
	}
	c, err := r.Cookie(auth.SessionCookieName)
	if err != nil {
		return auth.Principal{}, auth.ErrUnauthenticated
	}
	return a.Auth.Authenticate(ctx, c.Value)
}

func (a *GitHubArea) log() *slog.Logger {
	if a.Logger != nil {
		return a.Logger
	}
	return slog.Default()
}

// errorFor maps a service error to an HTTP status, a stable code and a
// message that is safe to show (never a secret, a path or a raw error).
func errorFor(err error) (int, string, string) {
	var (
		inv *auth.InvalidError
		na  *auth.NotAllowedError
		rl  *github.RateLimitError
		not *repos.ErrNotOrganization
		pub *repos.PublicOffError
	)
	switch {
	case errors.As(err, &pub):
		return http.StatusConflict, CodePublicRepositoriesOff, pub.Error()
	case errors.Is(err, auth.ErrUnauthenticated):
		return http.StatusUnauthorized, CodeUnauthenticated, "sign in first"
	case errors.Is(err, auth.ErrGitHubSessionExpired):
		return http.StatusUnauthorized, CodeSessionExpired, "sign in with GitHub again"
	case errors.As(err, &na):
		return http.StatusForbidden, CodeForbidden, na.Error()
	case errors.Is(err, auth.ErrForbidden), errors.Is(err, auth.ErrLoopbackOnly):
		var nad *repos.ErrNotAdmin
		if errors.As(err, &nad) {
			return http.StatusForbidden, CodeForbidden, nad.Error()
		}
		var noo *repos.ErrNotOrgOwner
		if errors.As(err, &noo) {
			return http.StatusForbidden, CodeForbidden, noo.Error()
		}
		return http.StatusForbidden, CodeForbidden, "your role does not allow this"
	case errors.Is(err, auth.ErrNotFound):
		return http.StatusNotFound, CodeNotFound, "not found"
	case errors.Is(err, auth.ErrNeedsConfirm), errors.Is(err, repos.ErrForkNeedsConfirm), errors.Is(err, repos.ErrFallbackNeedsConfirm):
		return http.StatusConflict, CodeNeedsConfirmation, err.Error()
	case errors.Is(err, auth.ErrConflict), errors.Is(err, repos.ErrPlanStale), errors.Is(err, repos.ErrPlanNotPlanned), errors.Is(err, repos.ErrPlanNotApplying),
		errors.Is(err, repos.ErrNothingToChange), errors.Is(err, auth.ErrSetupDone):
		return http.StatusConflict, CodeConflict, err.Error()
	case errors.Is(err, auth.ErrBadState), errors.Is(err, auth.ErrBadCode), errors.Is(err, auth.ErrInsecure), errors.As(err, &inv):
		return http.StatusBadRequest, CodeBadRequest, err.Error()
	case errors.As(err, &not):
		return http.StatusConflict, CodeConflict, not.Error()
	case errors.Is(err, auth.ErrNoApp):
		return http.StatusConflict, CodeConflict, "the GitHub App is not set up yet"
	case errors.As(err, &rl):
		return http.StatusTooManyRequests, CodeRateLimited, "GitHub is limiting requests until " + rl.ResetAt.UTC().Format("15:04 UTC")
	case errors.Is(err, github.ErrUnavailable):
		return http.StatusServiceUnavailable, CodeGitHubUnavailable, "GitHub could not be reached; nothing was changed"
	case errors.Is(err, secrets.ErrUnavailable):
		return http.StatusServiceUnavailable, CodeUnavailable, "the OS secret store is not available; see the doctor"
	case errors.Is(err, github.ErrForbidden):
		return http.StatusForbidden, CodeForbidden, "GitHub refused: a permission is missing"
	case errors.Is(err, github.ErrNotFound):
		return http.StatusNotFound, CodeNotFound, "GitHub says it is gone"
	}
	return http.StatusInternalServerError, CodeInternal, "internal error"
}

// failure builds the default response of a generated operation.
func failure(err error) (int, gen.ErrorEnvelope) {
	status, code, msg := errorFor(err)
	return status, gen.ErrorEnvelope{Error: gen.ErrorBody{Code: code, Message: msg}}
}

// origin is the console address the browser used.
func origin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func cookieValue(r *http.Request, name string) string {
	if r == nil {
		return ""
	}
	if c, err := r.Cookie(name); err == nil {
		return c.Value
	}
	return ""
}

func (a *GitHubArea) redirectWithError(to, code string, extra url.Values) gen.RedirectResponse {
	q := url.Values{"error": {code}}
	for k, v := range extra {
		q[k] = v
	}
	return gen.RedirectResponse{Headers: gen.RedirectResponseHeaders{Location: to + "?" + q.Encode()}}
}

func (s *server) ghArea() *GitHubArea { return s.opts.GitHub }

// GetSetupState implements GET /api/v1/setup.
func (s *server) GetSetupState(ctx context.Context, _ gen.GetSetupStateRequestObject) (gen.GetSetupStateResponseObject, error) {
	a := s.ghArea()
	st, err := a.Auth.State(ctx)
	if err != nil {
		code, body := failure(err)
		return gen.GetSetupStatedefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	return gen.GetSetupState200JSONResponse(a.setupState(ctx, requestFrom(ctx), st, s.machineJoined(ctx, a))), nil
}

// machineJoined is true when at least one machine has joined, in any state.
// It reads the same store as GET /api/v1/machines. A failed read is logged and
// counts as not yet joined.
func (s *server) machineJoined(ctx context.Context, a *GitHubArea) bool {
	if s.machinesOff() {
		return false
	}
	vs, err := s.opts.Machines.List(ctx)
	if err != nil {
		a.log().Warn("setup state: listing machines failed", "err", err)
		return false
	}
	return len(vs) > 0
}

func (a *GitHubArea) setupState(ctx context.Context, r *http.Request, st auth.SetupState, machineJoined bool) gen.SetupState {
	out := gen.SetupState{HasApp: st.HasApp, HasOwner: st.HasOwner,
		SetupLinkRedeemed: a.Auth.CheckSetupCookie(ctx, cookieValue(r, auth.SetupCookieName))}
	if st.HasApp {
		perms := st.App.Permissions
		out.App = &gen.GitHubApp{HtmlUrl: st.App.HTMLURL, Id: st.App.ID, OwnerLogin: st.App.OwnerLogin, Slug: st.App.Slug,
			WebhookActive: st.App.WebhookActive, Permissions: &perms}
	}
	out.Steps.ConnectGithub, out.Steps.FirstMachine, out.Steps.FirstRepository = "todo", "todo", "todo"
	if st.HasApp && st.HasOwner {
		out.Steps.ConnectGithub = "done"
	}
	if machineJoined || (a.HasMachine != nil && a.HasMachine(ctx)) {
		out.Steps.FirstMachine = "done"
	}
	if a.Repos != nil {
		hasPool, err := a.Repos.AnyPoolOn(ctx)
		if err != nil {
			a.log().Warn("setup state: listing pool-on repositories failed", "err", err)
		}
		if hasPool {
			out.Steps.FirstRepository = "done"
		}
	}
	return out
}

// RedeemSetupLink implements POST /api/v1/setup/redeem (R5.1).
func (s *server) RedeemSetupLink(ctx context.Context, req gen.RedeemSetupLinkRequestObject) (gen.RedeemSetupLinkResponseObject, error) {
	a := s.ghArea()
	r := requestFrom(ctx)
	if req.Body == nil {
		code, body := failure(auth.Invalid("a code is needed"))
		return gen.RedeemSetupLinkdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	cookie, err := a.Auth.RedeemSetupCode(ctx, req.Body.Code)
	if err != nil {
		code, body := failure(err)
		return gen.RedeemSetupLinkdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	c := &http.Cookie{Name: auth.SetupCookieName, Value: cookie, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: auth.CookieSecure(r), MaxAge: int(auth.SetupCookieTTL / time.Second)}
	st, _ := a.Auth.State(ctx)
	state := a.setupState(ctx, r, st, s.machineJoined(ctx, a))
	state.SetupLinkRedeemed = true
	sc := c.String()
	return gen.RedeemSetupLink200JSONResponse{Body: state, Headers: gen.RedeemSetupLink200ResponseHeaders{SetCookie: &sc}}, nil
}

// StartAppManifest implements POST /api/v1/setup/github-app (R5.2). It needs
// the setup cookie.
func (s *server) StartAppManifest(ctx context.Context, req gen.StartAppManifestRequestObject) (gen.StartAppManifestResponseObject, error) {
	a := s.ghArea()
	r := requestFrom(ctx)
	fail := func(err error) (gen.StartAppManifestResponseObject, error) {
		code, body := failure(err)
		return gen.StartAppManifestdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	if !a.Auth.CheckSetupCookie(ctx, cookieValue(r, auth.SetupCookieName)) {
		return fail(auth.ErrUnauthenticated)
	}
	if req.Body == nil {
		return fail(auth.Invalid("the request is empty"))
	}
	opts := auth.ManifestOptions{AppName: req.Body.AppName, ConsoleURLs: req.Body.ConsoleUrls}
	switch req.Body.AccountType {
	case gen.StartAppManifestRequestAccountTypeOrganization:
		if req.Body.Organization == nil || strings.TrimSpace(*req.Body.Organization) == "" {
			return fail(auth.Invalid("an organization login is needed"))
		}
		opts.Organization = strings.TrimSpace(*req.Body.Organization)
	}
	form, err := a.Auth.StartManifest(ctx, opts)
	if err != nil {
		return fail(err)
	}
	a.Auth.SetConsoleURLs(opts.ConsoleURLs)
	var m map[string]any
	_ = json.Unmarshal(form.Manifest.JSON(), &m)
	return gen.StartAppManifest200JSONResponse{Manifest: m, PostUrl: form.PostURL, State: form.State}, nil
}

// CompleteAppManifest implements GET /auth/github/app-created (R5.3).
func (s *server) CompleteAppManifest(ctx context.Context, req gen.CompleteAppManifestRequestObject) (gen.CompleteAppManifestResponseObject, error) {
	a := s.ghArea()
	if _, err := a.Auth.CompleteManifest(ctx, req.Params.Code, req.Params.State); err != nil {
		_, code, _ := errorFor(err)
		var extra url.Values
		var made *auth.AppCreatedError
		if errors.As(err, &made) && made.AppURL != "" {
			extra = url.Values{"app_url": {made.AppURL}}
		}
		return a.redirectWithError("/setup", code, extra), nil
	}
	return gen.RedirectResponse{Headers: gen.RedirectResponseHeaders{Location: "/setup?step=app-created"}}, nil
}

// signInFailure logs a failed sign-in at WARN with its code and cause (never a
// token or a code) and sends the browser to the sign-in page with that code
// (OPS17). stage is the step that failed.
func (a *GitHubArea) signInFailure(stage string, err error, extra url.Values) gen.RedirectResponse {
	code := signInCode(err)
	cause := err.Error()
	if len(cause) > 300 {
		cause = cause[:300]
	}
	a.log().Warn("sign-in failed", "stage", stage, "code", code, "cause", cause)
	return a.redirectWithError("/sign-in", code, extra)
}

// signInCode is the stable code of a sign-in failure. None maps to "internal".
func signInCode(err error) string {
	var se *auth.SignInError
	var na *auth.NotAllowedError
	switch {
	case errors.As(err, &se):
		return se.Code
	case errors.As(err, &na):
		return "not_allowed"
	case errors.Is(err, auth.ErrNoApp):
		return "no_app"
	case errors.Is(err, auth.ErrInsecure):
		return auth.SignInAddressRefused
	case errors.Is(err, auth.ErrBadCode):
		return "handoff_failed"
	case errors.Is(err, auth.ErrBadState):
		return auth.SignInStateMissing
	case errors.Is(err, secrets.ErrUnavailable):
		return "secret_store_unavailable"
	}
	return "sign_in_failed"
}

// StartSignIn implements GET /auth/github/login (R5.8).
func (s *server) StartSignIn(ctx context.Context, req gen.StartSignInRequestObject) (gen.StartSignInResponseObject, error) {
	a := s.ghArea()
	r := requestFrom(ctx)
	o := auth.SignInOptions{Origin: origin(r), SetupCookie: cookieValue(r, auth.SetupCookieName), SameMachine: sameMachine(r.RemoteAddr)}
	if req.Params.ReturnTo != nil {
		o.ReturnTo = *req.Params.ReturnTo
	}
	if req.Params.BindTray != nil {
		o.BindTray = *req.Params.BindTray
	}
	start, err := a.Auth.StartSignIn(ctx, o)
	if err != nil {
		var nr *auth.NotRegisteredError
		if errors.As(err, &nr) {
			// Never send the browser to a callback GitHub does not know:
			// the sign-in page shows the guided step instead (OPS16).
			q := url.Values{"address": {nr.Address}, "callback": {nr.CallbackURL}}
			if nr.AppURL != "" {
				q.Set("app_url", nr.AppURL)
			}
			return a.redirectWithError("/sign-in", "callback_not_registered", q), nil
		}
		return a.signInFailure("start", err, nil), nil
	}
	resp := gen.RedirectResponse{Headers: gen.RedirectResponseHeaders{Location: start.RedirectURL}}
	if start.HandoffBind != "" {
		c := (&http.Cookie{Name: auth.HandoffCookieName, Value: start.HandoffBind, Path: "/auth/github", HttpOnly: true,
			Secure: auth.CookieSecure(r), SameSite: http.SameSiteLaxMode, MaxAge: 600}).String()
		resp.Headers.SetCookie = &c
	}
	return resp, nil
}

// CompleteSignIn implements GET /auth/github/callback (R5.8 to R5.13).
func (s *server) CompleteSignIn(ctx context.Context, req gen.CompleteSignInRequestObject) (gen.CompleteSignInResponseObject, error) {
	a := s.ghArea()
	r := requestFrom(ctx)
	si, err := a.Auth.CompleteSignIn(ctx, req.Params.Code, req.Params.State, origin(r))
	if err != nil {
		var na *auth.NotAllowedError
		if errors.As(err, &na) {
			return a.signInFailure("callback", err, url.Values{"login": {na.Login}}), nil
		}
		var nr *auth.NotRegisteredError
		if errors.As(err, &nr) {
			q := url.Values{"address": {nr.Address}, "callback": {nr.CallbackURL}}
			if nr.AppURL != "" {
				q.Set("app_url", nr.AppURL)
			}
			a.log().Warn("sign-in failed", "stage", "callback", "code", "callback_not_registered", "cause", nr.Error())
			return a.redirectWithError("/sign-in", "callback_not_registered", q), nil
		}
		return a.signInFailure("callback", err, nil), nil
	}
	if si.Handoff != nil {
		// The session belongs to the address the sign-in started on.
		to := si.Handoff.Origin + "/auth/github/handoff?" + url.Values{"code": {si.Handoff.Code}, "state": {si.Handoff.State}}.Encode()
		return gen.RedirectResponse{Headers: gen.RedirectResponseHeaders{Location: to}}, nil
	}
	c := auth.SessionCookie(si.Cookie, auth.CookieSecure(r)).String()
	return gen.RedirectResponse{Headers: gen.RedirectResponseHeaders{Location: si.ReturnTo, SetCookie: &c}}, nil
}

// RedeemSignInHandoff implements GET /auth/github/handoff (OPS16).
func (s *server) RedeemSignInHandoff(ctx context.Context, req gen.RedeemSignInHandoffRequestObject) (gen.RedeemSignInHandoffResponseObject, error) {
	a := s.ghArea()
	r := requestFrom(ctx)
	si, err := a.Auth.RedeemHandoff(ctx, req.Params.Code, req.Params.State, origin(r), cookieValue(r, auth.HandoffCookieName))
	if err != nil {
		return a.signInFailure("handoff", err, nil), nil
	}
	c := auth.SessionCookie(si.Cookie, auth.CookieSecure(r)).String()
	return gen.RedirectResponse{Headers: gen.RedirectResponseHeaders{Location: si.ReturnTo, SetCookie: &c}}, nil
}

// CheckSignInAddress implements POST /api/v1/settings/signin-address (OPS16).
func (s *server) CheckSignInAddress(ctx context.Context, req gen.CheckSignInAddressRequestObject) (gen.CheckSignInAddressResponseObject, error) {
	fail := func(err error) (gen.CheckSignInAddressResponseObject, error) {
		code, body := failure(err)
		return gen.CheckSignInAddressdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	if req.Body == nil {
		return fail(auth.Invalid("the request is empty"))
	}
	addr := strings.TrimSpace(req.Body.Address)
	if !strings.Contains(addr, "://") {
		addr = "https://" + addr // a console address off this PC is https (R5.4)
	}
	got, err := s.ghArea().Auth.CheckSignInAddress(ctx, addr, true)
	if err != nil {
		return fail(err)
	}
	out := gen.CheckSignInAddress200JSONResponse{Address: got.Address, CallbackUrl: got.CallbackURL, Registered: got.Registered}
	if got.AppURL != "" {
		out.AppUrl = &got.AppURL
	}
	return out, nil
}

// sameMachine reports whether a request came from this PC: from a loopback
// address or from one of its own addresses. Such a browser can use the
// registered loopback callback (OPS16).
func sameMachine(remoteAddr string) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.Equal(ip) {
			return true
		}
	}
	return false
}

// RedeemTrayLink implements GET /auth/link (tray R5.7). Loopback only.
func (s *server) RedeemTrayLink(ctx context.Context, req gen.RedeemTrayLinkRequestObject) (gen.RedeemTrayLinkResponseObject, error) {
	a := s.ghArea()
	r := requestFrom(ctx)
	si, err := a.Auth.RedeemLink(ctx, req.Params.Code, r.RemoteAddr)
	if err != nil {
		// The sign-in page says the link expired and offers GitHub (tray R5.7).
		code := "link_expired"
		if errors.Is(err, auth.ErrLoopbackOnly) {
			code = "link_local_only"
		} else if !errors.Is(err, auth.ErrBadCode) {
			_, code, _ = errorFor(err)
		}
		return a.redirectWithError("/sign-in", code, nil), nil
	}
	c := auth.SessionCookie(si.Cookie, auth.CookieSecure(r)).String()
	return gen.RedirectResponse{Headers: gen.RedirectResponseHeaders{Location: si.ReturnTo, SetCookie: &c}}, nil
}

// GetCliLoginConfig implements GET /api/v1/auth/cli/config.
func (s *server) GetCliLoginConfig(ctx context.Context, _ gen.GetCliLoginConfigRequestObject) (gen.GetCliLoginConfigResponseObject, error) {
	a := s.ghArea()
	st, err := a.Auth.State(ctx)
	if err != nil || !st.HasApp {
		code, body := failure(auth.ErrNoApp)
		return gen.GetCliLoginConfigdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	base := a.WebBase
	if base == "" {
		base = addresses.DefaultGitHubURL
	}
	out := gen.GetCliLoginConfig200JSONResponse{ClientId: st.App.ClientID,
		DeviceCodeUrl: base + "/login/device/code", VerificationUrl: base + "/login/device"}
	if u := github.AppSettingsURL(base, st.App.Slug, st.App.OwnerType, st.App.OwnerLogin); u != "" {
		out.SettingsUrl = &u
	}
	return out, nil
}

func toCliToken(t auth.CLIToken) gen.CliToken {
	out := gen.CliToken{Id: t.ID, Name: t.Name, UserId: t.UserID, UserLogin: t.UserLogin, CreatedAt: t.CreatedAt, ExpiresAt: t.ExpiresAt}
	if !t.LastUsedAt.IsZero() {
		v := t.LastUsedAt
		out.LastUsedAt = &v
	}
	if !t.RevokedAt.IsZero() {
		v := t.RevokedAt
		out.RevokedAt = &v
	}
	return out
}

// ExchangeCliToken implements POST /api/v1/auth/cli (R5.14).
func (s *server) ExchangeCliToken(ctx context.Context, req gen.ExchangeCliTokenRequestObject) (gen.ExchangeCliTokenResponseObject, error) {
	a := s.ghArea()
	if req.Body == nil {
		code, body := failure(auth.Invalid("the request is empty"))
		return gen.ExchangeCliTokendefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	raw, info, err := a.Auth.CLIExchange(ctx, req.Body.GithubCredential, req.Body.Name)
	if err != nil {
		code, body := failure(err)
		return gen.ExchangeCliTokendefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	return gen.ExchangeCliToken201JSONResponse{Token: raw, Info: toCliToken(info)}, nil
}

func (s *server) principal(ctx context.Context) auth.Principal {
	p, _ := PrincipalFrom(ctx)
	return p
}

// ListCliTokens implements GET /api/v1/auth/cli/tokens.
func (s *server) ListCliTokens(ctx context.Context, _ gen.ListCliTokensRequestObject) (gen.ListCliTokensResponseObject, error) {
	ts, err := s.ghArea().Auth.ListCLITokens(ctx, s.principal(ctx))
	if err != nil {
		code, body := failure(err)
		return gen.ListCliTokensdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	items := make([]gen.CliToken, 0, len(ts))
	for _, t := range ts {
		items = append(items, toCliToken(t))
	}
	return gen.ListCliTokens200JSONResponse{Items: items}, nil
}

// RevokeCliToken implements DELETE /api/v1/auth/cli/tokens/{tokenId}.
func (s *server) RevokeCliToken(ctx context.Context, req gen.RevokeCliTokenRequestObject) (gen.RevokeCliTokenResponseObject, error) {
	if err := s.ghArea().Auth.RevokeCLIToken(ctx, s.principal(ctx), req.TokenId); err != nil {
		code, body := failure(err)
		return gen.RevokeCliTokendefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	return gen.RevokeCliToken204Response{}, nil
}

// SignOut implements POST /api/v1/auth/logout.
func (s *server) SignOut(ctx context.Context, _ gen.SignOutRequestObject) (gen.SignOutResponseObject, error) {
	if err := s.ghArea().Auth.SignOut(ctx, s.principal(ctx)); err != nil {
		code, body := failure(err)
		return gen.SignOutdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	return gen.SignOut204Response{}, nil
}

func toUser(u auth.User) gen.User {
	out := gen.User{Id: u.GitHubID, Login: u.Login, Role: gen.Role(u.Role), AddedAt: u.AddedAt}
	if u.AddedBy != "" {
		v := u.AddedBy
		out.AddedBy = &v
	}
	if !u.LastSignIn.IsZero() {
		v := u.LastSignIn
		out.LastSignInAt = &v
	}
	return out
}

// GetSession implements GET /api/v1/me.
func (s *server) GetSession(ctx context.Context, _ gen.GetSessionRequestObject) (gen.GetSessionResponseObject, error) {
	p := s.principal(ctx)
	u, err := s.ghArea().Auth.Me(ctx, p)
	if err != nil {
		code, body := failure(err)
		return gen.GetSessiondefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	gs := s.ghArea().Auth.GitHubSignInState(p.UserID)
	signin := gen.GitHubSignIn{State: gen.GitHubSignInStateEnded}
	if gs.Active {
		signin.State = gen.GitHubSignInStateActive
		if !gs.Expires.IsZero() {
			e := gs.Expires
			signin.ExpiresAt = &e
		}
	}
	return gen.GetSession200JSONResponse{User: toUser(u), Csrf: p.CSRF, GithubSignin: signin}, nil
}

// GetPreferences implements GET /api/v1/me/preferences.
func (s *server) GetPreferences(ctx context.Context, _ gen.GetPreferencesRequestObject) (gen.GetPreferencesResponseObject, error) {
	t, err := s.ghArea().Auth.Theme(ctx, s.principal(ctx))
	if err != nil {
		code, body := failure(err)
		return gen.GetPreferencesdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	return gen.GetPreferences200JSONResponse{Theme: gen.Theme(t)}, nil
}

// UpdatePreferences implements PUT /api/v1/me/preferences.
func (s *server) UpdatePreferences(ctx context.Context, req gen.UpdatePreferencesRequestObject) (gen.UpdatePreferencesResponseObject, error) {
	fail := func(err error) (gen.UpdatePreferencesResponseObject, error) {
		code, body := failure(err)
		return gen.UpdatePreferencesdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	if req.Body == nil {
		return fail(auth.Invalid("the request is empty"))
	}
	if err := s.ghArea().Auth.SetTheme(ctx, s.principal(ctx), string(req.Body.Theme)); err != nil {
		return fail(err)
	}
	return gen.UpdatePreferences200JSONResponse{Theme: req.Body.Theme}, nil
}

// ListUsers implements GET /api/v1/users.
func (s *server) ListUsers(ctx context.Context, _ gen.ListUsersRequestObject) (gen.ListUsersResponseObject, error) {
	us, err := s.ghArea().Auth.ListUsers(ctx, s.principal(ctx))
	if err != nil {
		code, body := failure(err)
		return gen.ListUsersdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	items := make([]gen.User, 0, len(us))
	for _, u := range us {
		items = append(items, toUser(u))
	}
	return gen.ListUsers200JSONResponse{Items: items}, nil
}

// AddUser implements POST /api/v1/users (R5.11).
func (s *server) AddUser(ctx context.Context, req gen.AddUserRequestObject) (gen.AddUserResponseObject, error) {
	fail := func(err error) (gen.AddUserResponseObject, error) {
		code, body := failure(err)
		return gen.AddUserdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	if req.Body == nil {
		return fail(auth.Invalid("the request is empty"))
	}
	u, err := s.ghArea().Auth.AddUser(ctx, s.principal(ctx), req.Body.Login, auth.Role(req.Body.Role))
	if err != nil {
		return fail(err)
	}
	return gen.AddUser201JSONResponse(toUser(u)), nil
}

// UpdateUserRole implements PATCH /api/v1/users/{userId} (R5.10).
func (s *server) UpdateUserRole(ctx context.Context, req gen.UpdateUserRoleRequestObject) (gen.UpdateUserRoleResponseObject, error) {
	fail := func(err error) (gen.UpdateUserRoleResponseObject, error) {
		code, body := failure(err)
		return gen.UpdateUserRoledefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	if req.Body == nil {
		return fail(auth.Invalid("the request is empty"))
	}
	confirm := req.Body.Confirm != nil && *req.Body.Confirm
	u, err := s.ghArea().Auth.UpdateRole(ctx, s.principal(ctx), req.UserId, auth.Role(req.Body.Role), confirm)
	if err != nil {
		return fail(err)
	}
	return gen.UpdateUserRole200JSONResponse(toUser(u)), nil
}

// RemoveUser implements DELETE /api/v1/users/{userId}.
func (s *server) RemoveUser(ctx context.Context, req gen.RemoveUserRequestObject) (gen.RemoveUserResponseObject, error) {
	if err := s.ghArea().Auth.RemoveUser(ctx, s.principal(ctx), req.UserId); err != nil {
		code, body := failure(err)
		return gen.RemoveUserdefaultJSONResponse{StatusCode: code, Body: body}, nil
	}
	return gen.RemoveUser204Response{}, nil
}

// ReceiveGithubWebhook is the strict-server twin of the webhook route. The
// route itself is served by HTTPMiddleware with the raw body; this answers
// when webhooks are wired off.
func (s *server) ReceiveGithubWebhook(_ context.Context, _ gen.ReceiveGithubWebhookRequestObject) (gen.ReceiveGithubWebhookResponseObject, error) {
	return gen.ReceiveGithubWebhookdefaultJSONResponse{StatusCode: http.StatusNotFound,
		Body: gen.ErrorEnvelope{Error: gen.ErrorBody{Code: CodeNotFound, Message: "webhooks are off for this controller"}}}, nil
}
