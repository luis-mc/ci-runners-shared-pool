package tests

import (
	"encoding/json"
	"io"
	"strings"
	"time"
)

type vitestParser struct{}

func (vitestParser) Format() Format { return Vitest }

func (vitestParser) Detect(head []byte) bool {
	return firstByte(head) == '{' && hasAny(head, `"testResults"`)
}

type vtFile struct {
	Name             string `json:"name"`
	AssertionResults []struct {
		Ancestors []string `json:"ancestorTitles"`
		Title     string   `json:"title"`
		Status    string   `json:"status"`
		Duration  float64  `json:"duration"`
		Retry     int      `json:"retryCount"`
	} `json:"assertionResults"`
}

// Parse streams one test file at a time. The vitest reporter does not report assertion counts: -1.
func (vitestParser) Parse(r io.Reader, emit func(Case) error) error {
	return streamRootArray(r, "testResults", func(dec *json.Decoder) error {
		var f vtFile
		if err := dec.Decode(&f); err != nil {
			return err
		}
		for _, a := range f.AssertionResults {
			c := Case{Suite: strings.Join(a.Ancestors, " > "), Name: a.Title, File: f.Name, Assertions: -1,
				Duration: time.Duration(a.Duration * float64(time.Millisecond)), Retries: a.Retry}
			switch a.Status {
			case "passed":
				c.Result = Passed
			case "failed":
				c.Result = Failed
			default:
				c.Result = Skipped
			}
			if err := emit(c); err != nil {
				return err
			}
		}
		return nil
	})
}
