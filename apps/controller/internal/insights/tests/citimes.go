package tests

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
)

type ciRunTimesParser struct{}

func (ciRunTimesParser) Format() Format { return CIRunTimes }

func (ciRunTimesParser) Detect(head []byte) bool {
	return firstByte(head) == '{' && hasAny(head, `"wallSeconds"`)
}

// Parse reads the `ci-run-times` artifact of dev-standards (ci_run_times.py): one case per job, named by the
// job, in suite "ci-run-times".
func (ciRunTimesParser) Parse(r io.Reader, emit func(Case) error) error {
	return streamRootArray(r, "jobs", func(dec *json.Decoder) error {
		var j struct {
			Name       string  `json:"name"`
			Conclusion string  `json:"conclusion"`
			Seconds    float64 `json:"seconds"`
		}
		if err := dec.Decode(&j); err != nil {
			return err
		}
		c := Case{Suite: "ci-run-times", Name: j.Name, Duration: secs(j.Seconds), Assertions: -1, Result: Passed}
		switch j.Conclusion {
		case "failure", "timed_out":
			c.Result = Failed
		case "cancelled", "skipped":
			c.Result = Skipped
		}
		return emit(c)
	})
}

type ciTimingsParser struct{}

func (ciTimingsParser) Format() Format { return CITimings }

var tableTimingsRE = regexp.MustCompile(`"seconds"\s*:\s*\{`)

var plainTimingsRE = regexp.MustCompile(`^\s*\{\s*"[^"]+"\s*:\s*[0-9]`)

func (ciTimingsParser) Detect(head []byte) bool {
	if firstByte(head) != '{' {
		return false
	}
	h := string(head)
	return tableTimingsRE.MatchString(h) ||
		plainTimingsRE.MatchString(h)
}

// Parse reads `scripts/delivery/ci-timings.json`: one case per test file, name and file the path.
func (ciTimingsParser) Parse(r io.Reader, emit func(Case) error) error {
	dec := json.NewDecoder(r)
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return fmt.Errorf("ci-timings: not an object")
	}
	return timingsObject(dec, true, emit)
}

// timingsObject reads key/number pairs; at the top level an object under "seconds" is the table.
func timingsObject(dec *json.Decoder, top bool, emit func(Case) error) error {
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return err
		}
		key, _ := kt.(string)
		if top && key == "seconds" {
			if t, err := dec.Token(); err == nil && t == json.Delim('{') {
				if err := timingsObject(dec, false, emit); err != nil {
					return err
				}
				continue
			}
			continue
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return err
		}
		var f float64
		if json.Unmarshal(v, &f) != nil {
			continue
		}
		if err := emit(Case{Suite: "ci-timings", Name: key, File: key, Duration: secs(f), Result: Passed, Assertions: -1}); err != nil {
			return err
		}
	}
	_, err := dec.Token() // the closing brace
	return err
}
