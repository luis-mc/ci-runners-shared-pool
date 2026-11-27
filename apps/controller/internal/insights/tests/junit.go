package tests

import (
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
)

type junitParser struct{}

func (junitParser) Format() Format { return JUnit }

func (junitParser) Detect(head []byte) bool {
	return firstByte(head) == '<' && hasAny(head, "<testsuite", "<testsuites") && !hasAny(head, "<TestRun")
}

// Parse streams <testcase> elements. Surefire rerun children (flakyFailure, rerunFailure, flakyError,
// rerunError) count as retries; a flaky case passed after them.
func (junitParser) Parse(r io.Reader, emit func(Case) error) error {
	dec := xml.NewDecoder(r)
	dec.Strict = false
	var suites []string
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("junit: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "testsuite":
				suites = append(suites, attr(t, "name"))
			case "testcase":
				c, err := junitCase(dec, t, suites)
				if err != nil {
					return err
				}
				if err := emit(c); err != nil {
					return err
				}
			}
		case xml.EndElement:
			if t.Name.Local == "testsuite" && len(suites) > 0 {
				suites = suites[:len(suites)-1]
			}
		}
	}
}

func junitCase(dec *xml.Decoder, start xml.StartElement, suites []string) (Case, error) {
	c := Case{Name: attr(start, "name"), File: attr(start, "file"), Result: Passed, Assertions: -1}
	c.Suite = attr(start, "classname")
	if c.Suite == "" && len(suites) > 0 {
		c.Suite = suites[len(suites)-1]
	}
	if v, err := strconv.ParseFloat(attr(start, "time"), 64); err == nil {
		c.Duration = secs(v)
	}
	if v, err := strconv.Atoi(attr(start, "assertions")); err == nil {
		c.Assertions = v
	}
	depth := 1
	for depth > 0 {
		tok, err := dec.Token()
		if err != nil {
			return c, fmt.Errorf("junit: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if depth != 2 {
				continue
			}
			switch strings.ToLower(t.Name.Local) {
			case "failure":
				c.Result = Failed
			case "error":
				c.Result = Error
			case "skipped":
				c.Result = Skipped
			case "flakyfailure", "flakyerror", "rerunfailure", "rerunerror":
				c.Retries++
			}
		case xml.EndElement:
			depth--
		}
	}
	return c, nil
}

func attr(e xml.StartElement, name string) string {
	for _, a := range e.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}
