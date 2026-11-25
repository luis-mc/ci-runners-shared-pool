package metrics

import (
	"fmt"
	"net"
	"strings"
)

// ScrapeConfig is the text to paste into prometheus.yml. host is the name or
// address Prometheus reaches this PC at; with the page on 127.0.0.1 it is
// localhost. The token appears as a placeholder, never as the real value.
func ScrapeConfig(c Config, host string) string {
	if host == "" || !c.Expose {
		host = "localhost"
	}
	var b strings.Builder
	b.WriteString("scrape_configs:\n  - job_name: crsp\n    scrape_interval: 30s\n    metrics_path: /metrics\n")
	if c.RequireToken {
		b.WriteString("    authorization:\n      type: Bearer\n      credentials: YOUR-TOKEN\n")
	}
	port := c.Port
	if port == 0 {
		port = 9464
	}
	fmt.Fprintf(&b, "    static_configs:\n      - targets: ['%s']\n", net.JoinHostPort(host, fmt.Sprint(port)))
	return b.String()
}
