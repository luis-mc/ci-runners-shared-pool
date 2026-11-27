package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

type apiApp struct {
	ID          int64             `json:"id"`
	Slug        string            `json:"slug"`
	ClientID    string            `json:"client_id"`
	HTMLURL     string            `json:"html_url"`
	Permissions map[string]string `json:"permissions"`
	Events      []string          `json:"events"`
	Owner       struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"owner"`
}

func (a apiApp) app() App {
	return App{ID: a.ID, Slug: a.Slug, ClientID: a.ClientID, OwnerLogin: a.Owner.Login,
		OwnerType: a.Owner.Type, HTMLURL: a.HTMLURL, Permissions: a.Permissions, Events: a.Events}
}

// ConvertManifest implements AppClient (R5.3). The code is single use on
// GitHub's side; the caller also refuses a second use.
func (c *REST) ConvertManifest(ctx context.Context, code string) (AppCredentials, error) {
	var out struct {
		apiApp
		ClientSecret  string `json:"client_secret"`
		WebhookSecret string `json:"webhook_secret"`
		PEM           string `json:"pem"`
	}
	if code == "" {
		return AppCredentials{}, errors.New("github: empty manifest code")
	}
	_, err := c.do(ctx, req{method: "POST", path: "/app-manifests/" + url.PathEscape(code) + "/conversions"}, &out)
	if err != nil {
		return AppCredentials{}, err
	}
	return AppCredentials{App: out.app(), PrivateKeyPEM: []byte(out.PEM), ClientSecret: out.ClientSecret, WebhookSecret: out.WebhookSecret}, nil
}

// GetApp implements AppClient (R5.6).
func (c *REST) GetApp(ctx context.Context) (App, error) {
	jwt, err := c.appJWT(ctx)
	if err != nil {
		return App{}, err
	}
	var out apiApp
	if _, err := c.do(ctx, req{method: "GET", path: "/app", bearer: jwt}, &out); err != nil {
		return App{}, err
	}
	return out.app(), nil
}

// AuthorizeURL implements OAuth (R5.8).
func (c *REST) AuthorizeURL(state, codeChallenge, redirectURI string) string {
	c.mu.Lock()
	clientID := c.clientID
	c.mu.Unlock()
	q := url.Values{}
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	q.Set("code_challenge", codeChallenge)
	q.Set("code_challenge_method", "S256")
	return c.cfg.WebBase + "/login/oauth/authorize?" + q.Encode()
}

// CheckRedirectURI asks GitHub's authorize page whether redirectURI is one of
// the App's callback URLs, without signing anyone in (a dry sign-in). GitHub
// answers 404 ("The redirect_uri is not associated with this application")
// for an address the App does not know, and a redirect to the login page or
// the page itself for a known one. No cookie is sent and no redirect is
// followed, so nothing is granted.
func (c *REST) CheckRedirectURI(ctx context.Context, redirectURI string) (bool, error) {
	c.mu.Lock()
	clientID := c.clientID
	c.mu.Unlock()
	if clientID == "" {
		return false, errNoApp
	}
	ctx, cancel := context.WithTimeout(ctx, CallDeadline)
	defer cancel()
	q := url.Values{}
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("state", "crsp-check")
	hr, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.WebBase+"/login/oauth/authorize?"+q.Encode(), nil)
	if err != nil {
		return false, err
	}
	hr.Header.Set("User-Agent", "crsp")
	hc := *c.cfg.HTTP
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := hc.Do(hr)
	if err != nil {
		return false, fmt.Errorf("%w: %v", ErrUnavailable, scrubURLError(err))
	}
	defer func() { _ = res.Body.Close() }()
	switch {
	case res.StatusCode == http.StatusNotFound:
		return false, nil
	case res.StatusCode < 400:
		return true, nil
	default:
		return false, fmt.Errorf("%w: GitHub answered %d to the sign-in check", ErrUnavailable, res.StatusCode)
	}
}

// ExchangeCode implements OAuth.
func (c *REST) ExchangeCode(ctx context.Context, code, codeVerifier, redirectURI string) (UserToken, error) {
	c.mu.Lock()
	appID, clientID := c.appID, c.clientID
	c.mu.Unlock()
	if appID == 0 {
		return UserToken{}, errNoApp
	}
	secret, err := c.cfg.Secrets.Get(ctx, SecretKey(appID, SecretClientSecret))
	if err != nil {
		return UserToken{}, fmt.Errorf("app client secret: %w", err)
	}
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
	}
	_, err = c.do(ctx, req{method: "POST", path: c.cfg.WebBase + "/login/oauth/access_token", abs: true, body: map[string]string{
		"client_id": clientID, "client_secret": string(secret), "code": code,
		"code_verifier": codeVerifier, "redirect_uri": redirectURI,
	}}, &out)
	if err != nil {
		return UserToken{}, err
	}
	if out.Error != "" || out.AccessToken == "" {
		// GitHub answers 200 with an error name for a bad or used code.
		return UserToken{}, fmt.Errorf("%w: sign-in code refused (%s)", ErrForbidden, out.Error)
	}
	tok := UserToken{Value: out.AccessToken}
	if out.ExpiresIn > 0 {
		tok.ExpiresAt = c.cfg.Now().Add(time.Duration(out.ExpiresIn) * time.Second)
	}
	return tok, nil
}

// CurrentUser implements OAuth.
func (c *REST) CurrentUser(ctx context.Context, tok UserToken) (User, error) {
	var out struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
	}
	if _, err := c.do(ctx, req{method: "GET", path: "/user", bearer: tok.Value}, &out); err != nil {
		return User{}, err
	}
	return User{ID: out.ID, Login: out.Login}, nil
}

// ResolveLogin implements OAuth (R5.11). It asks as the App.
func (c *REST) ResolveLogin(ctx context.Context, login string) (User, error) {
	jwt, err := c.appJWT(ctx)
	if err != nil {
		return User{}, err
	}
	var out struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
	}
	if _, err := c.do(ctx, req{method: "GET", path: "/users/" + url.PathEscape(login), bearer: jwt}, &out); err != nil {
		return User{}, err
	}
	return User{ID: out.ID, Login: out.Login}, nil
}

type apiInstallation struct {
	ID                  int64             `json:"id"`
	RepositorySelection string            `json:"repository_selection"`
	Permissions         map[string]string `json:"permissions"`
	SuspendedAt         *time.Time        `json:"suspended_at"`
	Account             struct {
		Login string `json:"login"`
		Type  string `json:"type"`
	} `json:"account"`
}

func (a apiInstallation) install() Installation {
	return Installation{ID: a.ID, AccountLogin: a.Account.Login, AccountType: a.Account.Type,
		RepositorySelection: a.RepositorySelection, Permissions: a.Permissions, Suspended: a.SuspendedAt != nil}
}

// ListUserInstallations implements UserClient (R5.17).
func (c *REST) ListUserInstallations(ctx context.Context, tok UserToken) ([]Installation, error) {
	var all []Installation
	err := c.getAll(ctx, req{path: "/user/installations", bearer: tok.Value}, func(raw json.RawMessage) error {
		var page struct {
			Installations []apiInstallation `json:"installations"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			return err
		}
		for _, i := range page.Installations {
			all = append(all, i.install())
		}
		return nil
	})
	return all, err
}

// ListInstallationRepositories implements UserClient (R5.18): all pages, with
// the user's admin flag.
func (c *REST) ListInstallationRepositories(ctx context.Context, tok UserToken, installationID int64) ([]Repository, error) {
	var all []Repository
	err := c.getAll(ctx, req{path: fmt.Sprintf("/user/installations/%d/repositories", installationID), bearer: tok.Value}, func(raw json.RawMessage) error {
		var page struct {
			Repositories []struct {
				ID            int64  `json:"id"`
				FullName      string `json:"full_name"`
				Name          string `json:"name"`
				Private       bool   `json:"private"`
				Visibility    string `json:"visibility"`
				DefaultBranch string `json:"default_branch"`
				Archived      bool   `json:"archived"`
				Owner         struct {
					Login string `json:"login"`
				} `json:"owner"`
				Permissions struct {
					Admin bool `json:"admin"`
				} `json:"permissions"`
			} `json:"repositories"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			return err
		}
		for _, r := range page.Repositories {
			vis := r.Visibility
			if vis == "" {
				vis = "public"
				if r.Private {
					vis = "private"
				}
			}
			all = append(all, Repository{ID: r.ID, FullName: r.FullName, Owner: r.Owner.Login, Name: r.Name,
				Visibility: vis, DefaultBranch: r.DefaultBranch, Archived: r.Archived, Admin: r.Permissions.Admin})
		}
		return nil
	})
	return all, err
}

// GetInstallation implements InstallationClient (R5.24).
func (c *REST) GetInstallation(ctx context.Context, installationID int64) (Installation, error) {
	jwt, err := c.appJWT(ctx)
	if err != nil {
		return Installation{}, err
	}
	var out apiInstallation
	if _, err := c.do(ctx, req{method: "GET", path: fmt.Sprintf("/app/installations/%d", installationID), bearer: jwt}, &out); err != nil {
		return Installation{}, err
	}
	return out.install(), nil
}

// UserCanAdmin reads the user's GitHub permission on one repository, fresh
// (R5.19). It uses the user's own token, so GitHub answers for that user.
func (c *REST) UserCanAdmin(ctx context.Context, tok UserToken, owner, repo string) (bool, error) {
	var out struct {
		Permissions struct {
			Admin bool `json:"admin"`
		} `json:"permissions"`
	}
	if _, err := c.do(ctx, req{method: "GET", path: fmt.Sprintf("/repos/%s/%s", url.PathEscape(owner), url.PathEscape(repo)), bearer: tok.Value}, &out); err != nil {
		return false, err
	}
	return out.Permissions.Admin, nil
}

// HookDelivery is one webhook delivery of the App as GitHub lists it.
type HookDelivery struct {
	ID          int64
	DeliveredAt time.Time
	Event       string
	// Status is GitHub's word ("OK", "Invalid HTTP Response: 502", ...).
	Status     string
	StatusCode int
}

// OK is true when the receiver answered with a 2xx status.
func (d HookDelivery) OK() bool { return d.StatusCode >= 200 && d.StatusCode < 300 }

// ListHookDeliveries lists the newest webhook deliveries of the App, newest
// first (doctor `github.webhook`). It needs no extra permission.
func (c *REST) ListHookDeliveries(ctx context.Context) ([]HookDelivery, error) {
	jwt, err := c.appJWT(ctx)
	if err != nil {
		return nil, err
	}
	var raw []struct {
		ID          int64     `json:"id"`
		DeliveredAt time.Time `json:"delivered_at"`
		Event       string    `json:"event"`
		Status      string    `json:"status"`
		StatusCode  int       `json:"status_code"`
	}
	if _, err := c.do(ctx, req{method: "GET", path: "/app/hook/deliveries", bearer: jwt, query: url.Values{"per_page": {"50"}}}, &raw); err != nil {
		return nil, err
	}
	out := make([]HookDelivery, 0, len(raw))
	for _, d := range raw {
		out = append(out, HookDelivery{ID: d.ID, DeliveredAt: d.DeliveredAt, Event: d.Event, Status: d.Status, StatusCode: d.StatusCode})
	}
	return out, nil
}

// CheckAppKey proves that a candidate private key belongs to the App: it signs
// an App JWT with the key, reads GET /app and requires the same App id
// (R5.7). The key is held only for this call; nothing is stored or logged.
func (c *REST) CheckAppKey(ctx context.Context, appID int64, pemBytes []byte) error {
	key, err := ParsePrivateKey(pemBytes)
	if err != nil {
		return ErrBadKey
	}
	jwt, err := SignAppJWT(key, appID, c.cfg.Now())
	if err != nil {
		return ErrBadKey
	}
	var out apiApp
	if _, err := c.do(ctx, req{method: "GET", path: "/app", bearer: jwt}, &out); err != nil {
		if errors.Is(err, ErrForbidden) || errors.Is(err, ErrNotFound) {
			return ErrKeyRefused
		}
		return err
	}
	if out.ID != appID {
		return ErrKeyRefused
	}
	return nil
}

// SetAppHook implements AppClient (OPS25): PATCH /app/hook/config.
func (c *REST) SetAppHook(ctx context.Context, h AppHook) error {
	return c.patchHook(ctx, map[string]string{"url": h.URL, "secret": h.Secret, "content_type": "json", "insecure_ssl": "0"})
}

// ClearAppHook implements AppClient (OPS25): an empty address and secret.
func (c *REST) ClearAppHook(ctx context.Context) error {
	return c.patchHook(ctx, map[string]string{"url": "", "secret": "", "content_type": "json", "insecure_ssl": "0"})
}

func (c *REST) patchHook(ctx context.Context, body map[string]string) error {
	jwt, err := c.appJWT(ctx)
	if err != nil {
		return err
	}
	_, err = c.do(ctx, req{method: "PATCH", path: "/app/hook/config", bearer: jwt, body: body, write: true}, nil)
	return err
}
