package tests

import (
	"encoding/json"
	"io"
	"strings"
	"time"
)

type playwrightParser struct{}

func (playwrightParser) Format() Format { return Playwright }

func (playwrightParser) Detect(head []byte) bool {
	return firstByte(head) == '{' && hasAny(head, `"config"`) && hasAny(head, `"suites"`) &&
		(hasAny(head, `"rootDir"`, `"configFile"`, `"projects"`, `"playwright`) || !hasAny(head, `"testResults"`))
}

type pwSuite struct {
	Title  string    `json:"title"`
	File   string    `json:"file"`
	Specs  []pwSpec  `json:"specs"`
	Suites []pwSuite `json:"suites"`
}

type pwSpec struct {
	Title string `json:"title"`
	File  string `json:"file"`
	Tests []struct {
		ProjectName string `json:"projectName"`
		Results     []struct {
			Status   string  `json:"status"`
			Duration float64 `json:"duration"`
			Retry    int     `json:"retry"`
		} `json:"results"`
	} `json:"tests"`
}

// Parse streams the top suites one at a time. A test's last result is its result; the others are retries.
// Playwright does not report assertions: -1.
func (playwrightParser) Parse(r io.Reader, emit func(Case) error) error {
	return streamRootArray(r, "suites", func(dec *json.Decoder) error {
		var s pwSuite
		if err := dec.Decode(&s); err != nil {
			return err
		}
		return walkPW(s, nil, emit)
	})
}

func walkPW(s pwSuite, path []string, emit func(Case) error) error {
	if s.Title != "" && s.Title != s.File {
		path = append(path[:len(path):len(path)], s.Title)
	}
	for _, sp := range s.Specs {
		file := sp.File
		if file == "" {
			file = s.File
		}
		for _, t := range sp.Tests {
			c := Case{Suite: strings.Join(path, " > "), Name: sp.Title, File: file, Assertions: -1, Result: Skipped}
			if len(t.Results) > 0 {
				last := t.Results[len(t.Results)-1]
				c.Result = pwResult(last.Status)
				c.Retries = len(t.Results) - 1
				c.Duration = time.Duration(last.Duration * float64(time.Millisecond))
			}
			if t.ProjectName != "" {
				c.Name += " [" + t.ProjectName + "]"
			}
			if err := emit(c); err != nil {
				return err
			}
		}
	}
	for _, sub := range s.Suites {
		if sub.File == "" {
			sub.File = s.File
		}
		if err := walkPW(sub, path, emit); err != nil {
			return err
		}
	}
	return nil
}

func pwResult(s string) Result {
	switch s {
	case "passed", "expected":
		return Passed
	case "skipped":
		return Skipped
	case "timedOut", "interrupted":
		return Error
	}
	return Failed
}
