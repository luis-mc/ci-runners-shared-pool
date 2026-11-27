package cli

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/client"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/cli/contexts"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/secrets"
)

// session is a context with its token and a client bound to it.
type session struct {
	ctx    contexts.Context
	token  storedToken
	client *client.ClientWithResponses
	raw    *client.Client
	base   string
}

// storedToken is what the secret store holds for a context. The id is not
// secret; it lets logout revoke the right token.
type storedToken struct {
	Token string `json:"token"`
	ID    string `json:"id"`
}

func tokenKey(name string) secrets.Key { return secrets.Key("crsp/cli/" + name) }

// newPinnedTransport builds an HTTPS transport that accepts only the pinned
// certificate (R5.5, R6.3). There is no flag that turns the check off.
func newPinnedTransport(fingerprint string) *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			// The controller's certificate is its own, not CA-signed. The
			// chain check is replaced by the fingerprint check below.
			InsecureSkipVerify: true, //nolint:gosec // see above
			VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
				if len(raw) == 0 {
					return errors.New("the controller sent no certificate")
				}
				sum := sha256.Sum256(raw[0])
				if !strings.EqualFold(hex.EncodeToString(sum[:]), fingerprint) {
					return errors.New("the controller certificate does not match the pinned fingerprint")
				}
				return nil
			},
		},
	}
}

// open returns the session for this call: context, token and client.
func (c *call) open() (*session, error) {
	if c.cached != nil {
		return c.cached, nil
	}
	cf, err := contexts.Load(c.env.ConfigDir)
	if err != nil {
		return nil, fail(ExitFailed, "Could not read the CLI config: %v", err)
	}
	var ctx contexts.Context
	if c.g.controller != "" {
		found, ok := cf.ByURL(c.g.controller)
		if !ok {
			return nil, fail(ExitUsage, "No context pins %s. Run crsp login %s first.", c.g.controller, c.g.controller)
		}
		ctx = found
	} else {
		ctx, err = cf.Pick(c.g.context, c.env.Getenv("CRSP_CONTEXT"))
		if err != nil {
			if errors.Is(err, contexts.ErrNoContext) {
				return nil, fail(ExitAuth, "Not signed in. Run crsp login URL.")
			}
			return nil, fail(ExitUsage, "%v. Run crsp context list.", err)
		}
	}
	raw, err := c.env.Secrets.Get(c.env.Context, tokenKey(ctx.Name))
	if errors.Is(err, secrets.ErrNotFound) {
		return nil, fail(ExitAuth, "Not signed in to %s. Run crsp login %s.", ctx.Name, ctx.URL)
	}
	if err != nil {
		return nil, fail(ExitFailed, "Could not read the token from the secret store: %v", err)
	}
	var tok storedToken
	if err := json.Unmarshal(raw, &tok); err != nil || tok.Token == "" {
		return nil, fail(ExitAuth, "The saved token for %s is unreadable. Run crsp login %s.", ctx.Name, ctx.URL)
	}
	s, err := c.newSession(ctx, tok)
	if err != nil {
		return nil, err
	}
	c.cached = s
	return s, nil
}

func (c *call) newSession(ctx contexts.Context, tok storedToken) (*session, error) {
	return c.newSessionWith(ctx, tok, func() http.RoundTripper { return newPinnedTransport(ctx.Fingerprint) })
}

// newSessionWith builds a session with the transport that pin() makes for an
// https address.
func (c *call) newSessionWith(ctx contexts.Context, tok storedToken, pin func() http.RoundTripper) (*session, error) {
	u, err := url.Parse(ctx.URL)
	if err != nil || !controllerSchemeOK(u) {
		return nil, fail(ExitUsage, "The controller address %q must start with https:// (http:// only for 127.0.0.1 or [::1]).", ctx.URL)
	}
	tr := pin()
	if u.Scheme == "http" {
		tr = &http.Transport{Proxy: nil}
	}
	base := strings.TrimRight(ctx.URL, "/")
	edit := func(_ context.Context, req *http.Request) error {
		if tok.Token != "" {
			req.Header.Set("Authorization", "Bearer "+tok.Token)
		}
		return nil
	}
	cl, err := client.NewClientWithResponses(base, client.WithHTTPClient(&http.Client{Transport: tr, Timeout: c.g.timeout}), client.WithRequestEditorFn(edit))
	if err != nil {
		return nil, fail(ExitFailed, "%v", err)
	}
	// The event stream has no overall timeout; it ends with Ctrl+C.
	rc, err := client.NewClient(base, client.WithHTTPClient(&http.Client{Transport: tr}), client.WithRequestEditorFn(edit))
	if err != nil {
		return nil, fail(ExitFailed, "%v", err)
	}
	return &session{ctx: ctx, token: tok, client: cl, raw: rc, base: base}, nil
}

// unreachable maps a transport error to exit 6 (R5.12).
func unreachable(base string, err error) error {
	msg := err.Error()
	if strings.Contains(msg, "pinned fingerprint") {
		return fail(ExitUnreachable, "The certificate of %s does not match the fingerprint pinned at login. It may be another machine. Nothing changed.", base)
	}
	return fail(ExitUnreachable, "Could not reach the controller at %s. %s. Make sure the address is right, then try again.", base, firstLine(msg))
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimRight(s, ". ")
}

// transport maps a transport error to an exitError.
func (s *session) transport(err error) error {
	if errors.Is(err, context.Canceled) {
		return fail(ExitFailed, "Stopped.")
	}
	return unreachable(s.base, err)
}

// apiError turns an error status into an exitError (R5.12, section 7).
func apiError(status int, body []byte) error {
	if status < 400 {
		return nil
	}
	var env client.ErrorEnvelope
	_ = json.Unmarshal(body, &env)
	msg := env.Error.Message
	if msg == "" {
		msg = fmt.Sprintf("The controller answered %d.", status)
	}
	if env.Error.Details != nil {
		for _, d := range *env.Error.Details {
			msg += "\n  " + d.Text
		}
	}
	code := ExitFailed
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		code = ExitAuth
	case http.StatusNotFound:
		code = ExitNotFound
	case http.StatusConflict:
		code = ExitConflict
	}
	return &exitError{Code: code, Message: msg, Envelope: body}
}

func writeEnvelope(w interface{ Write([]byte) (int, error) }, e *exitError) error {
	code := "failed"
	switch e.Code {
	case ExitUsage:
		code = "bad_request"
	case ExitAuth:
		code = "forbidden"
	case ExitNotFound:
		code = "not_found"
	case ExitNotConfirmed:
		code = "needs_confirmation"
	case ExitUnreachable:
		code = "unavailable"
	case ExitConflict:
		code = "conflict"
	}
	b, _ := json.Marshal(client.ErrorEnvelope{Error: client.ErrorBody{Code: code, Message: e.Message}})
	_, err := w.Write(append(b, '\n'))
	return err
}
