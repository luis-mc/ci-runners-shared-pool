package cli

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/client"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/cli/contexts"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/cli/output"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/secrets"
)

// fetchFingerprint dials host and returns the SHA-256 of its certificate. It
// is only used to show the fingerprint at first login (R5.5); no data is sent.
func fetchFingerprint(ctx context.Context, hostport string) (string, error) {
	d := tls.Dialer{Config: &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true}} //nolint:gosec // reads the certificate only, to show its fingerprint
	conn, err := d.DialContext(ctx, "tcp", hostport)
	if err != nil {
		return "", err
	}
	defer func() { _ = conn.Close() }()
	certs := conn.(*tls.Conn).ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return "", errors.New("the controller sent no certificate")
	}
	sum := sha256.Sum256(certs[0].Raw)
	return hex.EncodeToString(sum[:]), nil
}

func normFingerprint(s string) string {
	return strings.ToLower(strings.NewReplacer(":", "", " ", "").Replace(s))
}

// controllerSchemeOK: https for any host, plain http for 127.0.0.1 and [::1]
// only (D38).
func controllerSchemeOK(u *url.URL) bool {
	if u == nil || u.Host == "" {
		return false
	}
	switch u.Scheme {
	case "https":
		return true
	case "http":
		ip := net.ParseIP(u.Hostname())
		return ip != nil && (ip.Equal(net.IPv4(127, 0, 0, 1)) || ip.Equal(net.IPv6loopback))
	}
	return false
}

func hostPort(u *url.URL) string {
	if u.Port() != "" {
		return u.Host
	}
	return net.JoinHostPort(u.Hostname(), "443")
}

func cmdLogin() *command {
	var name, fingerprint, tokenName string
	return &command{
		name: "login", summary: "sign in with GitHub's device flow", args: "[URL]",
		flags: func(c *call, fs *flag.FlagSet) {
			fs.StringVar(&name, "name", "", "context name (default: the controller's host name)")
			fs.StringVar(&fingerprint, "fingerprint", "", "expected certificate SHA-256 fingerprint (skips the question)")
			fs.StringVar(&tokenName, "token-name", "", "label for this token in the controller's list")
		},
		run: func(c *call, args []string) error {
			cf, err := contexts.Load(c.env.ConfigDir)
			if err != nil {
				return fail(ExitFailed, "Could not read the CLI config: %v", err)
			}
			raw := ""
			if len(args) > 0 {
				raw = args[0]
			} else if cur, err := cf.Pick(c.g.context, c.env.Getenv("CRSP_CONTEXT")); err == nil {
				raw = cur.URL
			}
			if raw == "" {
				return fail(ExitUsage, "Which controller? Run crsp login https://HOST:PORT (http://127.0.0.1:PORT on the controller machine).")
			}
			u, err := url.Parse(raw)
			if err != nil || !controllerSchemeOK(u) {
				return fail(ExitUsage, "The controller address %q must start with https:// (http:// only for 127.0.0.1 or [::1]).", raw)
			}
			base := strings.TrimRight(u.String(), "/")
			if name == "" {
				name = u.Hostname()
			}
			// Certificate pin (R5.5).
			fp := ""
			if existing, ok := cf.ByURL(base); ok {
				fp = existing.Fingerprint
			}
			// A loopback controller speaks plain HTTP: no certificate, no pin.
			if fp == "" && u.Scheme == "https" {
				seen, err := fetchFingerprint(c.env.Context, hostPort(u))
				if err != nil {
					return unreachable(base, err)
				}
				if fingerprint != "" {
					if normFingerprint(fingerprint) != seen {
						return fail(ExitUnreachable, "The certificate of %s has fingerprint %s, not the one you gave. Nothing changed.", base, seen)
					}
				} else if err := c.confirm("The controller "+base+" shows this certificate (SHA-256):", []string{seen}, false); err != nil {
					return err
				}
				fp = seen
			}
			s, err := c.newSession(contexts.Context{Name: name, URL: base, Fingerprint: fp}, storedToken{})
			if err != nil {
				return err
			}
			cfg, err := s.client.GetCliLoginConfigWithResponse(c.env.Context)
			if err != nil {
				return unreachable(base, err)
			}
			if err := apiError(cfg.StatusCode(), cfg.Body); err != nil {
				return err
			}
			if cfg.JSON200 == nil {
				return fail(ExitFailed, "The controller sent no sign-in settings.")
			}
			ghToken, err := c.deviceFlow(*cfg.JSON200)
			if err != nil {
				return err
			}
			label := tokenName
			if label == "" {
				label, _ = os.Hostname()
				if label == "" {
					label = "crsp-cli"
				}
			}
			ex, err := s.client.ExchangeCliTokenWithResponse(c.env.Context, client.ExchangeCliTokenJSONRequestBody{GithubCredential: ghToken, Name: label})
			if err != nil {
				return unreachable(base, err)
			}
			if err := apiError(ex.StatusCode(), ex.Body); err != nil {
				return err
			}
			if ex.JSON201 == nil {
				return fail(ExitFailed, "The controller sent no token.")
			}
			val, _ := json.Marshal(storedToken{Token: ex.JSON201.Token, ID: ex.JSON201.Info.Id})
			if err := c.env.Secrets.Put(c.env.Context, tokenKey(name), val); err != nil {
				return fail(ExitFailed, "Could not save the token in the secret store (%s): %v. Nothing was saved.", c.env.Secrets.Backend(), err)
			}
			cf.Set(contexts.Context{Name: name, URL: base, Fingerprint: fp})
			if err := cf.Save(c.env.ConfigDir); err != nil {
				return fail(ExitFailed, "Could not write the CLI config: %v", err)
			}
			if c.g.json {
				return output.JSON(c.env.Stdout, map[string]string{"context": name, "url": base, "user": ex.JSON201.Info.UserLogin})
			}
			_, _ = fmt.Fprintf(c.env.Stderr, "Signed in to %s as %s.\n", name, ex.JSON201.Info.UserLogin)
			return nil
		},
	}
}

// deviceFlow runs GitHub's device flow (decision 0002) and returns the GitHub
// user token. The token is passed to the controller once and never stored.
func (c *call) deviceFlow(cfg client.CliLoginConfig) (string, error) {
	gh := &http.Client{Timeout: c.g.timeout}
	post := func(rawURL string, form url.Values, out any) error {
		req, err := http.NewRequestWithContext(c.env.Context, http.MethodPost, rawURL, strings.NewReader(form.Encode()))
		if err != nil {
			return err
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := gh.Do(req)
		if err != nil {
			return err
		}
		defer func() { _ = resp.Body.Close() }()
		return json.NewDecoder(resp.Body).Decode(out)
	}
	var dc struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		ExpiresIn       int    `json:"expires_in"`
		Interval        int    `json:"interval"`
	}
	if err := post(cfg.DeviceCodeUrl, url.Values{"client_id": {cfg.ClientId}}, &dc); err != nil || dc.DeviceCode == "" {
		return "", fail(ExitUnreachable, "Could not start GitHub's device sign-in. Try again in a minute.")
	}
	link := dc.VerificationURI
	if link == "" {
		link = cfg.VerificationUrl
	}
	_, _ = fmt.Fprintf(c.env.Stderr, "Open %s and enter this code: %s\nWaiting for you to approve...\n", link, dc.UserCode)
	du, err := url.Parse(cfg.DeviceCodeUrl)
	if err != nil {
		return "", fail(ExitFailed, "The controller sent a bad device code address.")
	}
	tokenURL := du.Scheme + "://" + du.Host + "/login/oauth/access_token"
	interval := time.Duration(dc.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	if c.env.PollEvery > 0 {
		interval = c.env.PollEvery
	}
	deadline := c.env.Now().Add(time.Duration(max(dc.ExpiresIn, 900)) * time.Second)
	for {
		select {
		case <-c.env.Context.Done():
			return "", fail(ExitFailed, "Stopped.")
		case <-time.After(interval):
		}
		var tr struct {
			AccessToken string `json:"access_token"`
			Error       string `json:"error"`
		}
		err := post(tokenURL, url.Values{
			"client_id":   {cfg.ClientId},
			"device_code": {dc.DeviceCode},
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		}, &tr)
		if err != nil {
			return "", fail(ExitUnreachable, "Could not reach GitHub while waiting for you. Try again.")
		}
		switch tr.Error {
		case "", "authorization_pending":
		case "slow_down":
			interval += 5 * time.Second
		case "access_denied":
			return "", fail(ExitAuth, "GitHub sign-in was refused.")
		case "expired_token":
			return "", fail(ExitAuth, "The code expired. Run crsp login again.")
		default:
			return "", fail(ExitFailed, "GitHub sign-in failed: %s.", tr.Error)
		}
		if tr.AccessToken != "" {
			return tr.AccessToken, nil
		}
		if c.env.Now().After(deadline) {
			return "", fail(ExitAuth, "The code expired. Run crsp login again.")
		}
	}
}

func cmdLogout() *command {
	return &command{
		name: "logout", summary: "forget this context's token", args: "",
		run: func(c *call, _ []string) error {
			cf, err := contexts.Load(c.env.ConfigDir)
			if err != nil {
				return fail(ExitFailed, "Could not read the CLI config: %v", err)
			}
			ctx, err := cf.Pick(c.g.context, c.env.Getenv("CRSP_CONTEXT"))
			if err != nil {
				return fail(ExitAuth, "Not signed in.")
			}
			raw, err := c.env.Secrets.Get(c.env.Context, tokenKey(ctx.Name))
			if errors.Is(err, secrets.ErrNotFound) {
				return fail(ExitAuth, "Not signed in to %s. There is no token to forget.", ctx.Name)
			}
			if err != nil {
				return fail(ExitFailed, "Could not read the secret store: %v", err)
			}
			var tok storedToken
			_ = json.Unmarshal(raw, &tok)
			revoked := false
			var why string
			if s, err := c.newSession(ctx, tok); err != nil {
				why = err.Error()
			} else if tok.ID != "" {
				r, err := s.client.RevokeCliTokenWithResponse(c.env.Context, tok.ID)
				switch {
				case err != nil:
					why = s.transport(err).Error()
				case apiError(r.StatusCode(), r.Body) != nil:
					why = apiError(r.StatusCode(), r.Body).Error()
				default:
					revoked = true
				}
			}
			// Delete locally even when the controller cannot be reached (R5.8).
			if err := c.env.Secrets.Delete(c.env.Context, tokenKey(ctx.Name)); err != nil {
				return fail(ExitFailed, "Could not delete the token from the secret store: %v", err)
			}
			if revoked {
				_, _ = fmt.Fprintf(c.env.Stderr, "Signed out of %s. The token is revoked.\n", ctx.Name)
			} else {
				_, _ = fmt.Fprintf(c.env.Stderr, "Signed out of %s. The token is deleted here, but the controller was not told: %s\n", ctx.Name, firstLine(why))
			}
			return nil
		},
	}
}

func cmdOpen() *command {
	var printOnly bool
	return &command{
		name: "open", summary: "print and open a one-time console link", args: "[--print]",
		flags: func(c *call, fs *flag.FlagSet) { fs.BoolVar(&printOnly, "print", false, "only print the link") },
		run: func(c *call, _ []string) error {
			s, err := c.open()
			if err != nil {
				return err
			}
			r, err := s.client.CreateConsoleLinkWithResponse(c.env.Context)
			if err != nil {
				return s.transport(err)
			}
			if err := apiError(r.StatusCode(), r.Body); err != nil {
				return err
			}
			if r.JSON201 == nil {
				return fail(ExitFailed, "The controller sent no link.")
			}
			if c.g.json {
				return output.JSON(c.env.Stdout, r.JSON201)
			}
			_, _ = fmt.Fprintln(c.env.Stdout, r.JSON201.Url)
			_, _ = fmt.Fprintln(c.env.Stderr, "This link works once and expires in 60 seconds.")
			if !printOnly && c.env.OpenBrowser != nil {
				if err := c.env.OpenBrowser(r.JSON201.Url); err != nil {
					_, _ = fmt.Fprintln(c.env.Stderr, "No desktop session found. Open the link yourself.")
				}
			}
			return nil
		},
	}
}

func cmdContext() *command {
	return &command{
		name: "context", summary: "choose between controllers",
		subs: []*command{
			{name: "list", summary: "list contexts", run: func(c *call, _ []string) error {
				cf, err := contexts.Load(c.env.ConfigDir)
				if err != nil {
					return fail(ExitFailed, "Could not read the CLI config: %v", err)
				}
				if c.g.json {
					type row struct {
						Name        string `json:"name"`
						URL         string `json:"url"`
						Fingerprint string `json:"fingerprint"`
						Current     bool   `json:"current"`
					}
					rows := []row{}
					for _, n := range cf.Names() {
						x := cf.Contexts[n]
						rows = append(rows, row{n, x.URL, x.Fingerprint, n == cf.Current})
					}
					return output.JSON(c.env.Stdout, map[string]any{"items": rows})
				}
				if len(cf.Contexts) == 0 {
					_, _ = fmt.Fprintln(c.env.Stderr, "No contexts yet. Run crsp login URL to add one.")
					return nil
				}
				var rows [][]string
				for _, n := range cf.Names() {
					mark := ""
					if n == cf.Current {
						mark = "*"
					}
					rows = append(rows, []string{mark, n, cf.Contexts[n].URL})
				}
				output.Table(c.env.Stdout, output.MaxWidth, []string{"CURRENT", "NAME", "URL"}, rows)
				return nil
			}},
			{name: "use", summary: "make a context current", args: "NAME", run: func(c *call, args []string) error {
				if len(args) != 1 {
					return fail(ExitUsage, "Usage: crsp context use NAME")
				}
				cf, err := contexts.Load(c.env.ConfigDir)
				if err != nil {
					return fail(ExitFailed, "Could not read the CLI config: %v", err)
				}
				if err := cf.Use(args[0]); err != nil {
					return fail(ExitNotFound, "%v. Run crsp context list.", err)
				}
				if err := cf.Save(c.env.ConfigDir); err != nil {
					return fail(ExitFailed, "Could not write the CLI config: %v", err)
				}
				_, _ = fmt.Fprintf(c.env.Stderr, "Now using %s.\n", args[0])
				return nil
			}},
			{name: "remove", summary: "delete a context and its token", args: "NAME [--yes]", flags: func(c *call, fs *flag.FlagSet) {
				fs.Bool("yes", false, "do not ask")
			}, run: func(c *call, args []string) error {
				if len(args) != 1 {
					return fail(ExitUsage, "Usage: crsp context remove NAME [--yes]")
				}
				cf, err := contexts.Load(c.env.ConfigDir)
				if err != nil {
					return fail(ExitFailed, "Could not read the CLI config: %v", err)
				}
				x, ok := cf.Contexts[args[0]]
				if !ok {
					return fail(ExitNotFound, "No context named %q. Run crsp context list.", args[0])
				}
				yes := c.fs.Lookup("yes").Value.String() == "true"
				if err := c.confirm("This removes context "+x.Name+" and its saved token:", []string{x.Name + " (" + x.URL + ")"}, yes); err != nil {
					return err
				}
				if err := c.env.Secrets.Delete(c.env.Context, tokenKey(x.Name)); err != nil {
					return fail(ExitFailed, "Could not delete the token: %v", err)
				}
				_ = cf.Remove(x.Name)
				if err := cf.Save(c.env.ConfigDir); err != nil {
					return fail(ExitFailed, "Could not write the CLI config: %v", err)
				}
				_, _ = fmt.Fprintf(c.env.Stderr, "Context %s removed.\n", x.Name)
				return nil
			}},
		},
	}
}
