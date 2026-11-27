package compare

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

//go:embed prices.json
var pricesJSON []byte

// Prices is the per-minute price table of GitHub-hosted runners.
type Prices struct {
	DefaultPerMinute float64            `json:"default_per_minute"`
	PerMinute        map[string]float64 `json:"per_minute"`
}

// DefaultPrices loads the table embedded from prices.json, the one config file.
func DefaultPrices() (Prices, error) {
	return ParsePrices(pricesJSON)
}

// ParsePrices reads a price table and refuses a negative or missing default.
func ParsePrices(b []byte) (Prices, error) {
	var p Prices
	if err := json.Unmarshal(b, &p); err != nil {
		return Prices{}, fmt.Errorf("compare: read prices: %w", err)
	}
	if p.DefaultPerMinute <= 0 {
		return Prices{}, fmt.Errorf("compare: prices need a default_per_minute above zero")
	}
	for k, v := range p.PerMinute {
		if v < 0 {
			return Prices{}, fmt.Errorf("compare: price for %q is negative", k)
		}
	}
	return p, nil
}

// For returns the per-minute price of the first label with a price, else the default.
func (p Prices) For(labels []string) float64 {
	for _, l := range labels {
		if v, ok := p.PerMinute[strings.ToLower(l)]; ok {
			return v
		}
	}
	return p.DefaultPerMinute
}
