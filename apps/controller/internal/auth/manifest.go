package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/github"
)

// Permissions is the exact set the App asks for, and no others (R5.23). The
// keys are GitHub's manifest permission names.
var Permissions = github.RequiredPermissions

// Events the App subscribes to.
var Events = []string{"workflow_job", "repository"}

var appNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 _-]{0,33}$`)

// Manifest is GitHub's App manifest.
type Manifest struct {
	Name                  string            `json:"name"`
	URL                   string            `json:"url"`
	RedirectURL           string            `json:"redirect_url"`
	CallbackURLs          []string          `json:"callback_urls"`
	SetupURL              string            `json:"setup_url"`
	SetupOnUpdate         bool              `json:"setup_on_update"`
	Public                bool              `json:"public"`
	HookAttributes        *HookAttributes   `json:"hook_attributes,omitempty"`
	DefaultEvents         []string          `json:"default_events,omitempty"`
	DefaultPermissions    map[string]string `json:"default_permissions"`
	RequestOAuthOnInstall bool              `json:"request_oauth_on_install"`
}

// HookAttributes is the webhook part of the manifest (R5.5).
type HookAttributes struct {
	URL    string `json:"url"`
	Active bool   `json:"active"`
}

// ManifestForm is what the wizard posts to GitHub.
type ManifestForm struct {
	Manifest Manifest
	PostURL  string
	State    string
}

// ManifestOptions are the wizard's answers (R5.2).
type ManifestOptions struct {
	AppName     string
	ConsoleURLs []string
	// Organization is empty for a personal account.
	Organization string
	// WebhookURL is a public HTTPS URL; empty keeps webhooks off. It is used
	// only when some console address is not loopback.
	WebhookURL string
}

// BuildManifest makes the manifest. The redirect URL is where GitHub sends
// the browser with the one-time code; it is the first console address.
func BuildManifest(o ManifestOptions) (Manifest, error) {
	if !appNameRE.MatchString(o.AppName) {
		return Manifest{}, Invalid("app name: use letters, numbers, spaces, dashes and underscores, up to 34 characters")
	}
	cbs, err := CallbackURLs(o.ConsoleURLs)
	if err != nil {
		return Manifest{}, err
	}
	first := strings.TrimSuffix(cbs[0], CallbackPath)
	m := Manifest{
		Name:                  o.AppName,
		URL:                   first,
		RedirectURL:           first + "/auth/github/app-created",
		CallbackURLs:          cbs,
		SetupURL:              first + "/repositories",
		SetupOnUpdate:         true,
		Public:                false,
		DefaultPermissions:    map[string]string{},
		RequestOAuthOnInstall: false,
	}
	for k, v := range Permissions {
		m.DefaultPermissions[k] = v
	}
	// Webhooks stay off for a local-only controller (R5.5). GitHub refuses a
	// manifest whose hook URL is not public, even an inactive one, so a
	// controller without a public webhook URL sends no hook and no events and
	// finds jobs by polling (R5.37).
	if !AllLoopback(o.ConsoleURLs) && o.WebhookURL != "" {
		u, err := url.Parse(o.WebhookURL)
		if err != nil || u.Scheme != "https" || u.Host == "" || IsLoopbackHost(u.Host) {
			return Manifest{}, Invalid("webhook URL must be a public https address")
		}
		m.HookAttributes = &HookAttributes{URL: o.WebhookURL, Active: true}
		m.DefaultEvents = append([]string(nil), Events...)
	}
	return m, nil
}

// JSON is the manifest as GitHub reads it.
func (m Manifest) JSON() []byte {
	b, _ := json.Marshal(m)
	return b
}

// StartManifest makes the manifest and a single-use state valid for 10
// minutes (R5.2). The caller has checked the setup cookie.
func (s *Service) StartManifest(ctx context.Context, o ManifestOptions) (ManifestForm, error) {
	if _, err := s.store.GetApp(ctx); err == nil {
		return ManifestForm{}, ErrSetupDone
	}
	m, err := BuildManifest(o)
	if err != nil {
		return ManifestForm{}, err
	}
	origins := make([]string, 0, len(m.CallbackURLs))
	for _, cb := range m.CallbackURLs {
		origins = append(origins, strings.TrimSuffix(cb, CallbackPath))
	}
	state := s.states.put("manifest", StateTTL, map[string]string{
		"webhook":  fmt.Sprint(m.HookAttributes != nil && m.HookAttributes.Active),
		"consoles": strings.Join(origins, ","),
	})
	post := s.cfg.WebBase + "/settings/apps/new"
	if o.Organization != "" {
		post = s.cfg.WebBase + "/organizations/" + url.PathEscape(o.Organization) + "/settings/apps/new"
	}
	return ManifestForm{Manifest: m, PostURL: post + "?state=" + url.QueryEscape(state), State: state}, nil
}

// CompleteManifest handles GitHub's redirect: it checks the state, trades the
// code once, puts the secrets in the OS store and keeps only public facts in
// the database (R5.3). A second use of the same code or state is refused.
func (s *Service) CompleteManifest(ctx context.Context, code, state string) (AppRecord, error) {
	data, ok := s.states.take("manifest", state)
	if !ok {
		s.audit(ctx, "system", "setup.app_failed", "github_app", "", "bad or old state", "failed")
		return AppRecord{}, ErrBadState
	}
	if _, err := s.store.GetApp(ctx); err == nil {
		return AppRecord{}, ErrSetupDone
	}
	s.mu.Lock()
	if s.usedManifestCodes[Hash(code)] {
		s.mu.Unlock()
		return AppRecord{}, ErrBadCode
	}
	s.usedManifestCodes[Hash(code)] = true
	s.mu.Unlock()

	creds, err := s.cfg.GitHub.ConvertManifest(ctx, code)
	if err != nil {
		s.audit(ctx, "system", "setup.app_failed", "github_app", "", "conversion failed", "failed")
		return AppRecord{}, err
	}
	id := creds.App.ID
	// Secrets first. If the store fails nothing is kept and the person starts
	// again; the database never holds a half-made App. GitHub gives an empty
	// webhook secret to an App made without a hook, and a store may refuse an
	// empty value (Windows DPAPI does), so that one is kept only when it exists.
	parts := []struct {
		name string
		val  []byte
	}{{"private-key", creds.PrivateKeyPEM}, {"client-secret", []byte(creds.ClientSecret)}}
	if creds.WebhookSecret != "" {
		parts = append(parts, struct {
			name string
			val  []byte
		}{"webhook-secret", []byte(creds.WebhookSecret)})
	}
	for _, kv := range parts {
		if err := s.cfg.Secrets.Put(ctx, secretKey(id, kv.name), kv.val); err != nil {
			return AppRecord{}, s.halfMade(ctx, creds.App, fmt.Errorf("storing the App secrets: %w", err))
		}
	}
	rec := AppRecord{ID: id, Slug: creds.App.Slug, ClientID: creds.App.ClientID, OwnerLogin: creds.App.OwnerLogin,
		OwnerType: creds.App.OwnerType, HTMLURL: creds.App.HTMLURL, WebhookActive: data["webhook"] == "true",
		Permissions: creds.App.Permissions}
	if err := s.store.SaveApp(ctx, rec); err != nil {
		return AppRecord{}, s.halfMade(ctx, creds.App, err)
	}
	if l := data["consoles"]; l != "" && s.cfg.SaveRegistered != nil {
		_ = s.cfg.SaveRegistered(ctx, strings.Split(l, ","))
	}
	if s.cfg.OnApp != nil {
		s.cfg.OnApp(rec)
	}
	s.audit(ctx, "setup", "setup.app_created", "github_app", rec.Slug, "", "done")
	return rec, nil
}

// AppCreatedError is a failure after GitHub made the App: the App exists at
// GitHub but the controller kept nothing. AppURL is the App's page when it is
// an https://github.com address, so the person can delete the App.
type AppCreatedError struct {
	AppURL string
	Err    error
}

func (e *AppCreatedError) Error() string {
	return "GitHub made the App but it was not saved: " + e.Err.Error()
}
func (e *AppCreatedError) Unwrap() error { return e.Err }

// SafeAppURL returns u when it is an https://github.com address with no
// credentials, else "". The page shows it as a link, so nothing else passes.
func SafeAppURL(u string) string {
	p, err := url.Parse(u)
	if err != nil || p.Scheme != "https" || p.Host != "github.com" || p.User != nil {
		return ""
	}
	return p.String()
}

func (s *Service) halfMade(ctx context.Context, app github.App, err error) error {
	u := SafeAppURL(app.HTMLURL)
	s.audit(ctx, "system", "setup.app_incomplete", "github_app", app.Slug, "GitHub made the App but it was not saved: "+u, "failed")
	return &AppCreatedError{AppURL: u, Err: err}
}
