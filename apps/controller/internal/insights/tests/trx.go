package tests

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"
	"time"
)

type trxParser struct{}

func (trxParser) Format() Format { return TRX }

func (trxParser) Detect(head []byte) bool {
	return firstByte(head) == '<' && hasAny(head, "<TestRun")
}

// Parse streams <UnitTestResult> elements. TRX counters are run totals, not per test, so a case's assertions
// are "not reported" (-1), never 0 (R5.37).
func (trxParser) Parse(r io.Reader, emit func(Case) error) error {
	dec := xml.NewDecoder(r)
	dec.Strict = false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("trx: %w", err)
		}
		t, ok := tok.(xml.StartElement)
		if !ok || t.Name.Local != "UnitTestResult" {
			continue
		}
		name := attr(t, "testName")
		suite := ""
		if i := strings.LastIndex(name, "."); i > 0 && !strings.Contains(name, "(") {
			suite, name = name[:i], name[i+1:]
		}
		c := Case{Suite: suite, Name: name, Assertions: -1, Duration: trxDuration(attr(t, "duration"))}
		switch strings.ToLower(attr(t, "outcome")) {
		case "passed":
			c.Result = Passed
		case "failed", "timeout", "aborted":
			c.Result = Failed
		case "error":
			c.Result = Error
		default:
			c.Result = Skipped
		}
		if err := emit(c); err != nil {
			return err
		}
	}
}

// trxDuration reads "hh:mm:ss.fffffff".
func trxDuration(s string) time.Duration {
	var h, m int
	var sec float64
	if _, err := fmt.Sscanf(s, "%d:%d:%f", &h, &m, &sec); err != nil {
		return 0
	}
	return time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + secs(sec)
}
