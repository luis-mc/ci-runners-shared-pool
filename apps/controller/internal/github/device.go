package github

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"time"
)

// DeviceCode is GitHub's answer to a device flow start (R5.14).
type DeviceCode struct {
	DeviceCode      string
	UserCode        string
	VerificationURI string
	Interval        time.Duration
	ExpiresIn       time.Duration
}

// ErrDeviceDenied: the person said no on GitHub, or the code ran out.
var ErrDeviceDenied = errors.New("github: device sign-in was not approved")

// StartDevice begins the device flow with the App's client id. It needs no
// secret, so the CLI can call it.
func (c *REST) StartDevice(ctx context.Context) (DeviceCode, error) {
	c.mu.Lock()
	clientID := c.clientID
	c.mu.Unlock()
	var out struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		ExpiresIn       int    `json:"expires_in"`
		Interval        int    `json:"interval"`
	}
	_, err := c.do(ctx, req{method: "POST", path: c.cfg.WebBase + "/login/device/code", abs: true,
		body: map[string]string{"client_id": clientID}}, &out)
	if err != nil {
		return DeviceCode{}, err
	}
	if out.DeviceCode == "" {
		return DeviceCode{}, errors.New("github: device flow refused; turn on device flow in the App settings")
	}
	d := DeviceCode{DeviceCode: out.DeviceCode, UserCode: out.UserCode, VerificationURI: out.VerificationURI,
		Interval: time.Duration(out.Interval) * time.Second, ExpiresIn: time.Duration(out.ExpiresIn) * time.Second}
	if d.Interval <= 0 {
		d.Interval = 5 * time.Second
	}
	return d, nil
}

// WaitDevice polls at the interval GitHub gave until the person approves, says
// no, or the code runs out. On slow_down it adds 5 seconds as GitHub asks.
func (c *REST) WaitDevice(ctx context.Context, d DeviceCode) (UserToken, error) {
	c.mu.Lock()
	clientID := c.clientID
	c.mu.Unlock()
	interval := d.Interval
	deadline := c.cfg.Now().Add(d.ExpiresIn)
	for {
		if d.ExpiresIn > 0 && !c.cfg.Now().Before(deadline) {
			return UserToken{}, ErrDeviceDenied
		}
		if err := c.cfg.Sleep(ctx, interval); err != nil {
			return UserToken{}, err
		}
		var out struct {
			AccessToken string `json:"access_token"`
			Error       string `json:"error"`
			ExpiresIn   int    `json:"expires_in"`
		}
		_, err := c.do(ctx, req{method: "POST", path: c.cfg.WebBase + "/login/oauth/access_token", abs: true,
			body: map[string]string{"client_id": clientID, "device_code": d.DeviceCode,
				"grant_type": "urn:ietf:params:oauth:grant-type:device_code"}}, &out)
		if err != nil {
			return UserToken{}, err
		}
		switch out.Error {
		case "":
			if out.AccessToken == "" {
				return UserToken{}, errors.New("github: empty device token")
			}
			tok := UserToken{Value: out.AccessToken}
			if out.ExpiresIn > 0 {
				tok.ExpiresAt = c.cfg.Now().Add(time.Duration(out.ExpiresIn) * time.Second)
			}
			return tok, nil
		case "authorization_pending":
		case "slow_down":
			interval += 5 * time.Second
		default:
			return UserToken{}, fmt.Errorf("%w (%s)", ErrDeviceDenied, out.Error)
		}
	}
}

// CheckUserToken asks GitHub whether a user token was made for this App
// (POST /applications/{client_id}/token) and returns its user (R5.14). The
// controller calls this once on the token the CLI sends, then drops it.
func (c *REST) CheckUserToken(ctx context.Context, tok UserToken) (User, error) {
	c.mu.Lock()
	appID, clientID := c.appID, c.clientID
	c.mu.Unlock()
	if appID == 0 {
		return User{}, errNoApp
	}
	secret, err := c.cfg.Secrets.Get(ctx, SecretKey(appID, SecretClientSecret))
	if err != nil {
		return User{}, fmt.Errorf("app client secret: %w", err)
	}
	basic := base64.StdEncoding.EncodeToString([]byte(clientID + ":" + string(secret)))
	var out struct {
		User struct {
			ID    int64  `json:"id"`
			Login string `json:"login"`
		} `json:"user"`
	}
	_, err = c.do(ctx, req{method: "POST", path: "/applications/" + url.PathEscape(clientID) + "/token",
		headers: map[string]string{"Authorization": "Basic " + basic},
		body:    map[string]string{"access_token": tok.Value}}, &out)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return User{}, fmt.Errorf("%w: the token is not for this App", ErrForbidden)
		}
		return User{}, err
	}
	return User{ID: out.User.ID, Login: out.User.Login}, nil
}
