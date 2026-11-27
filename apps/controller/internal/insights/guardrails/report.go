package guardrails

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/insights"
)

// maxReport is the most bytes of a guard report Parse reads (the collector caps a file at 50 MiB, R5.3).
const maxReport = 16 << 20

// All is G1 to G7 in order.
var All = []insights.Guardrail{"G1", "G2", "G3", "G4", "G5", "G6", "G7"}

// ReportParser reads the JSON of `test_guardrails.py --json` (R5.31).
type ReportParser struct{}

var _ Parser = ReportParser{}

// Detect says whether head has the shape of the report: a JSON object with "results" and "mode" or "G1".
func (ReportParser) Detect(head []byte) bool {
	h := bytes.TrimLeft(head, "\xef\xbb\xbf \t\r\n")
	return len(h) > 0 && h[0] == '{' && bytes.Contains(h, []byte(`"results"`)) &&
		(bytes.Contains(h, []byte(`"mode"`)) || bytes.Contains(h, []byte(`"G1"`)))
}

type rawBreak struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Message string `json:"message"`
}

type rawEntry struct {
	Title      string          `json:"title"`
	State      string          `json:"state"`
	Reason     string          `json:"reason"`
	Count      int             `json:"count"`
	Breaks     []rawBreak      `json:"breaks"`
	KnownCount json.RawMessage `json:"known_count"`
	Known      json.RawMessage `json:"known"`
}

type rawReport struct {
	Mode       string              `json:"mode"`
	Results    map[string]rawEntry `json:"results"`
	KnownCount json.RawMessage     `json:"known_count"`
	Known      json.RawMessage     `json:"known"`
}

// Parse reads one report. The known-list count is the report's own "known_count" (a number) or "known" (a
// list or a number), else the sum of the guardrails' own, else -1.
func (ReportParser) Parse(r io.Reader) (Report, error) {
	var raw rawReport
	if err := json.NewDecoder(io.LimitReader(r, maxReport)).Decode(&raw); err != nil {
		return Report{}, fmt.Errorf("guard report: %w", err)
	}
	if raw.Results == nil {
		return Report{}, fmt.Errorf("guard report: no results")
	}
	rep := Report{Mode: raw.Mode, KnownCount: knownOf(raw.KnownCount, raw.Known)}
	perEntry, sum := false, 0
	keys := make([]string, 0, len(raw.Results))
	for k := range raw.Results {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		g := insights.Guardrail(k)
		if !valid(g) {
			return Report{}, fmt.Errorf("guard report: unknown guardrail %q", k)
		}
		re := raw.Results[k]
		st := State(re.State)
		switch st {
		case Pass, Broken, Off:
		default:
			return Report{}, fmt.Errorf("guard report: %s has state %q", k, re.State)
		}
		e := Entry{Guardrail: g, State: st, Count: re.Count, Reason: re.Reason}
		for _, b := range re.Breaks {
			e.Breaks = append(e.Breaks, Break(b))
		}
		if e.Count == 0 && len(e.Breaks) > 0 {
			e.Count = len(e.Breaks)
		}
		if n := knownOf(re.KnownCount, re.Known); n >= 0 {
			perEntry = true
			sum += n
		}
		rep.Entries = append(rep.Entries, e)
	}
	if rep.KnownCount < 0 && perEntry {
		rep.KnownCount = sum
	}
	return rep, nil
}

func valid(g insights.Guardrail) bool {
	for _, x := range All {
		if x == g {
			return true
		}
	}
	return false
}

// knownOf reads a count from a number, or the length of a list or an object; -1 when absent.
func knownOf(vals ...json.RawMessage) int {
	for _, v := range vals {
		v = bytes.TrimSpace(v)
		if len(v) == 0 || string(v) == "null" {
			continue
		}
		if n, err := strconv.Atoi(string(v)); err == nil && n >= 0 {
			return n
		}
		var list []json.RawMessage
		if json.Unmarshal(v, &list) == nil {
			return len(list)
		}
		var obj map[string]json.RawMessage
		if json.Unmarshal(v, &obj) == nil {
			return len(obj)
		}
	}
	return -1
}
