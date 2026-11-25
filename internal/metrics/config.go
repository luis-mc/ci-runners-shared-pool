package metrics

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// Defaults.
const (
	DefaultAddr        = "127.0.0.1:9464"
	DefaultPushSeconds = 60
	MinPushSeconds     = 10
	MaxPushSeconds     = 3600
	MinTokenLength     = 16
	DefaultServiceName = "crsp"
	metricPrefix       = "crsp_"
)

// Config is what the owner sets. Everything is off by default. The token is
// not part of Config: it lives in the secret store.
type Config struct {
	// Enabled turns the /metrics page on.
	Enabled bool `json:"enabled"`
	// Port is the TCP port of the page.
	Port int `json:"port"`
	// Expose lets the page listen on every address of the PC. Off: 127.0.0.1 only.
	Expose bool `json:"expose"`
	// RequireToken makes the page ask for a bearer token.
	RequireToken bool `json:"require_token"`
	// OTLP push.
	PushEnabled  bool   `json:"push_enabled"`
	PushEndpoint string `json:"push_endpoint"` // for example http://collector:4318
	PushSeconds  int    `json:"push_seconds"`
}

// Default is the off state.
func Default() Config {
	return Config{Port: 9464, PushSeconds: DefaultPushSeconds}
}

// ListenAddr is where the page listens: loopback unless Expose.
func (c Config) ListenAddr() string {
	host := "127.0.0.1"
	if c.Expose {
		host = "0.0.0.0"
	}
	port := c.Port
	if port == 0 {
		port = 9464
	}
	return net.JoinHostPort(host, fmt.Sprint(port))
}

// Validate returns the problems of c, one sentence each.
func (c Config) Validate() []string {
	var p []string
	if c.Port < 1024 || c.Port > 65535 {
		p = append(p, "The port must be between 1024 and 65535.")
	}
	if c.PushSeconds < MinPushSeconds || c.PushSeconds > MaxPushSeconds {
		p = append(p, fmt.Sprintf("The push interval must be between %d and %d seconds.", MinPushSeconds, MaxPushSeconds))
	}
	if c.PushEnabled {
		if _, err := OTLPURL(c.PushEndpoint); err != nil {
			p = append(p, err.Error())
		}
	}
	return p
}

// OTLPURL turns an endpoint into the metrics URL of OTLP over HTTP. A bare
// collector address gets /v1/metrics.
func OTLPURL(endpoint string) (string, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return "", errors.New("Type the address of the OpenTelemetry collector.")
	}
	u, err := url.Parse(endpoint)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", errors.New("The collector address must start with http:// or https://.")
	}
	if u.User != nil {
		return "", errors.New("Do not put a password in the collector address.")
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/v1/metrics"
	}
	return u.String(), nil
}
