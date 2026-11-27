package tests

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"strings"
)

type goTestParser struct{}

func (goTestParser) Format() Format { return GoTest }

func (goTestParser) Detect(head []byte) bool {
	return firstByte(head) == '{' && hasAny(head, `"Action"`) && hasAny(head, `"Package"`)
}

type goEvent struct {
	Action  string
	Package string
	Test    string
	Elapsed float64
}

// Parse reads one JSON event per line, from an artifact or from a job log (lines that are not JSON events are
// skipped). A test ends on pass, fail or skip; go test does not report assertions: -1.
func (goTestParser) Parse(r io.Reader, emit func(Case) error) error {
	br := bufio.NewReaderSize(r, 64*1024)
	for {
		line, err := br.ReadBytes('\n')
		line = bytes.TrimSpace(line)
		if len(line) > 0 && line[0] == '{' {
			var e goEvent
			if json.Unmarshal(line, &e) == nil && e.Test != "" {
				if c, ok := goCase(e); ok {
					if err := emit(c); err != nil {
						return err
					}
				}
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func goCase(e goEvent) (Case, bool) {
	c := Case{Suite: e.Package, Name: e.Test, Assertions: -1, Duration: secs(e.Elapsed)}
	switch e.Action {
	case "pass":
		c.Result = Passed
	case "fail":
		c.Result = Failed
	case "skip":
		c.Result = Skipped
	default:
		return c, false
	}
	if i := strings.LastIndex(e.Package, "/"); i >= 0 {
		c.File = e.Package[i+1:]
	}
	return c, true
}
