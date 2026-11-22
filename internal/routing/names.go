// Package routing gives each CI job of a repository its own routing label and
// repository variable (github-integration R5.101 to R5.108, routing option A).
//
// A job named e2e in repository o/r gets the label crsp-e2e-<hash> and the
// variable CRSP_E2E; the variable holds the JSON list of labels the job asks
// for. Workflows read it with a fallback, so a job nothing is known about is
// never blocked. This package holds the pure logic (names, label plan, runner
// choice, workflow patch) and the service that keeps variables and runner
// labels; it reaches GitHub, the scan and the store through small interfaces
// so every test runs on fakes.
package routing

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"sort"
	"strings"
)

// Limits and prefixes.
const (
	// LabelCap is the most labels one runner may carry, read-only ones
	// included; GitHub fails the 101st add with HTTP 500.
	LabelCap = 100
	// VarPrefix starts every per-job variable.
	VarPrefix = "CRSP_"
	// LabelPrefix starts every routing label (a generic label, runners R5.36).
	LabelPrefix = "crsp-"
	maxSlug     = 24
)

// routingLabelRE is crsp-<slug>-<4 hex>.
var routingLabelRE = regexp.MustCompile(`^crsp-[a-z0-9]+(-[a-z0-9]+)*-[0-9a-f]{4}$`)

// IsLabel reports whether a label has the shape of a routing label.
func IsLabel(l string) bool { return routingLabelRE.MatchString(strings.ToLower(l)) }

// IsJobVar reports whether a variable name is a per-job variable.
func IsJobVar(name string) bool { return strings.HasPrefix(name, VarPrefix) }

// StripLabels drops routing labels from a list.
func StripLabels(labels []string) []string {
	var out []string
	for _, l := range labels {
		if !IsLabel(l) {
			out = append(out, l)
		}
	}
	return out
}

func shortHash(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:2])
}

// slug lowercases s, turns every run of characters outside a-z and 0-9 into
// one dash, trims dashes and cuts to max characters; an empty result is "job".
func slug(s string, max int) string {
	var b strings.Builder
	dash := true
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > max {
		out = strings.Trim(out[:max], "-")
	}
	if out == "" {
		return "job"
	}
	return out
}

// JobLabel is the label of one job: crsp-<job>-<short hash of repository and job>.
func JobLabel(repository, job string) string {
	return LabelPrefix + slug(job, maxSlug) + "-" + shortHash(repository, "job:"+job)
}

// ClassLabel is the label several jobs of one size class share when the
// per-job labels would pass the cap: crsp-<class>-<hash>, e.g. crsp-large-4c1d.
func ClassLabel(repository, class string) string {
	return LabelPrefix + slug(class, maxSlug) + "-" + shortHash(repository, "class:"+class)
}

// VarNames gives each job key its variable name: CRSP_<JOB>. Keys that
// normalise to the same name get the short hash of the later key added. The
// result depends only on the set of keys, so the workflow patch, the sync and
// the doctor all agree.
func VarNames(jobs []string) map[string]string {
	keys := append([]string(nil), jobs...)
	sort.Strings(keys)
	out := map[string]string{}
	taken := map[string]bool{}
	for _, k := range keys {
		if _, dup := out[k]; dup {
			continue
		}
		n := VarPrefix + strings.ToUpper(strings.ReplaceAll(slug(k, 40), "-", "_"))
		if taken[n] {
			n += "_" + strings.ToUpper(shortHash(k))
		}
		taken[n] = true
		out[k] = n
	}
	return out
}
