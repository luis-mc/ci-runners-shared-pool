package auth

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// NotRegisteredError is a sign-in or an address change that would send the
// browser to a callback URL GitHub does not know for the App. GitHub has no
// API to add one, so the person adds it in the App's settings (OPS16).
type NotRegisteredError struct {
	Address     string // console origin, such as https://192.168.1.162:8460
	CallbackURL string // the exact URL to add
	AppURL      string // the App's settings page on GitHub; empty when unknown
}

func (e *NotRegisteredError) Error() string {
	return fmt.Sprintf("GitHub does not know the sign-in address %s; add %s to the GitHub App", e.Address, e.CallbackURL)
}

// registered returns the normalized origins the App has callback URLs for.
func (s *Service) registered(ctx context.Context) []string {
	var out []string
	for _, a := range s.consoleURLs(ctx) {
		if n, err := NormalizeConsoleURL(a); err == nil {
			out = append(out, n)
		}
	}
	return out
}

func (s *Service) isRegistered(ctx context.Context, origin string) bool {
	for _, n := range s.registered(ctx) {
		if n == origin {
			return true
		}
	}
	return false
}

// AddRegistered keeps an origin GitHub was seen to accept.
func (s *Service) addRegistered(ctx context.Context, origin string) {
	if s.cfg.SaveRegistered == nil || s.isRegistered(ctx, origin) {
		return
	}
	_ = s.cfg.SaveRegistered(ctx, append(s.registered(ctx), origin))
}

// loopbackRegistered is the registered loopback origin a browser on this PC
// can always reach.
func (s *Service) loopbackRegistered(ctx context.Context) string {
	for _, n := range s.registered(ctx) {
		if u, err := url.Parse(n); err == nil && IsLoopbackHost(u.Host) {
			return n
		}
	}
	return ""
}

// chooseRedirect picks the redirect_uri of a sign-in that started on origin.
// handoff is true when the OAuth round trip goes through a loopback address.
func (s *Service) chooseRedirect(ctx context.Context, origin string, sameMachine bool) (redirect string, handoff bool, err error) {
	if s.isRegistered(ctx, origin) {
		return origin + CallbackPath, false, nil
	}
	if sameMachine {
		if lb := s.loopbackRegistered(ctx); lb != "" {
			return lb + CallbackPath, true, nil
		}
	}
	// A dry sign-in: the address may have been added to the App since.
	if ok, perr := s.cfg.GitHub.CheckRedirectURI(ctx, origin+CallbackPath); perr == nil && ok {
		s.addRegistered(ctx, origin)
		return origin + CallbackPath, false, nil
	}
	return "", false, s.notRegistered(ctx, origin)
}

func (s *Service) notRegistered(ctx context.Context, origin string) error {
	return &NotRegisteredError{Address: origin, CallbackURL: origin + CallbackPath, AppURL: s.appSettingsURL(ctx)}
}

// appSettingsURL is the page where the App's callback URLs are edited.
func (s *Service) appSettingsURL(ctx context.Context) string {
	a, err := s.store.GetApp(ctx)
	if err != nil || a.Slug == "" {
		return ""
	}
	base := strings.TrimSuffix(s.cfg.WebBase, "/")
	slug := url.PathEscape(a.Slug)
	if strings.EqualFold(a.OwnerType, "Organization") && a.OwnerLogin != "" {
		return base + "/organizations/" + url.PathEscape(a.OwnerLogin) + "/settings/apps/" + slug
	}
	return base + "/settings/apps/" + slug
}

// SignInAddress is the answer of the guided step "Add this sign-in address to
// your GitHub App".
type SignInAddress struct {
	Address     string
	CallbackURL string
	AppURL      string
	// Registered is true when GitHub is known to accept the callback URL.
	Registered bool
}

// CheckSignInAddress tells whether the console origin can sign in. With probe
// it also asks GitHub (a dry sign-in) when the controller does not know the
// address yet, and keeps a confirmed one. Never signs anyone in.
func (s *Service) CheckSignInAddress(ctx context.Context, origin string, probe bool) (SignInAddress, error) {
	o, err := NormalizeConsoleURL(origin)
	if err != nil {
		return SignInAddress{}, err
	}
	if _, err := s.store.GetApp(ctx); err != nil {
		// No App yet: the manifest of the setup lists the addresses.
		return SignInAddress{Address: o, CallbackURL: o + CallbackPath, Registered: true}, nil
	}
	out := SignInAddress{Address: o, CallbackURL: o + CallbackPath, AppURL: s.appSettingsURL(ctx), Registered: s.isRegistered(ctx, o)}
	if !out.Registered && probe {
		ok, err := s.cfg.GitHub.CheckRedirectURI(ctx, out.CallbackURL)
		if err != nil {
			return out, err
		}
		if ok {
			s.addRegistered(ctx, o)
			out.Registered = true
		}
	}
	return out, nil
}
