package collect

import (
	"bytes"
	"strings"
)

// Report formats the collector recognises by content (R5.3, R5.31, R5.33). The names are those of
// internal/insights/tests (Format) and "guardrails" for the guard report.
const (
	FormatGuardrails = "guardrails"
	FormatJUnit      = "junit"
	FormatTRX        = "trx"
	FormatPlaywright = "playwright"
	FormatVitest     = "vitest"
	FormatGoTest     = "gotest"
	FormatLibtest    = "libtest"
)

// HeadBytes is how much of a file the detectors see.
const HeadBytes = 64 << 10

// Detector says whether the first bytes of a file are one report format. The wiring may add the Detect of
// the parsers in internal/insights/tests; DefaultDetectors are the collector's own.
type Detector struct {
	Format string
	Detect func(head []byte) bool
}

// DefaultDetectors recognise the formats of R5.33 and the guard report of R5.31 by their content, most
// specific first. A file's name never decides.
func DefaultDetectors() []Detector {
	has := func(s string, parts ...string) bool {
		for _, p := range parts {
			if !strings.Contains(s, p) {
				return false
			}
		}
		return true
	}
	return []Detector{
		{FormatGuardrails, func(h []byte) bool {
			s := string(h)
			return startsWith(h, '{') && has(s, `"results"`, `"mode"`, `"G1"`)
		}},
		{FormatTRX, func(h []byte) bool {
			return has(string(h), "<TestRun", "VisualStudio/TeamTest")
		}},
		{FormatJUnit, func(h []byte) bool {
			return startsWith(h, '<') && strings.Contains(string(h), "<testsuite")
		}},
		{FormatPlaywright, func(h []byte) bool {
			s := string(h)
			return startsWith(h, '{') && has(s, `"suites"`, `"config"`)
		}},
		{FormatVitest, func(h []byte) bool {
			s := string(h)
			return startsWith(h, '{') && (strings.Contains(s, `"numTotalTestSuites"`) || has(s, `"testResults"`, `"numTotalTests"`))
		}},
		{FormatGoTest, func(h []byte) bool {
			l := firstLine(h)
			return strings.HasPrefix(l, `{"Time":`) && strings.Contains(l, `"Action"`)
		}},
		{FormatLibtest, func(h []byte) bool {
			l := strings.ReplaceAll(firstLine(h), " ", "")
			return strings.HasPrefix(l, `{"type":"suite"`) && strings.Contains(l, `"event"`)
		}},
	}
}

func startsWith(h []byte, c byte) bool {
	h = bytes.TrimLeft(bytes.TrimPrefix(h, []byte("\xef\xbb\xbf")), " \t\r\n")
	return len(h) > 0 && h[0] == c
}

func firstLine(h []byte) string {
	h = bytes.TrimLeft(bytes.TrimPrefix(h, []byte("\xef\xbb\xbf")), " \t\r\n")
	if i := bytes.IndexByte(h, '\n'); i >= 0 {
		h = h[:i]
	}
	return strings.TrimSpace(string(h))
}

// Classify returns the format of a file from its head, or "" when it is none of them.
func Classify(head []byte, ds []Detector) string {
	for _, d := range ds {
		if d.Detect(head) {
			return d.Format
		}
	}
	return ""
}
