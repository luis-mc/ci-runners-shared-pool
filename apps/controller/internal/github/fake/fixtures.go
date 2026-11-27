package fake

import (
	"embed"
	"encoding/json"
	"fmt"
)

//go:embed fixtures/*.json
var fixtureFS embed.FS

// Fixture returns a scrubbed, recorded-shape fixture by name (R6.9). Fixtures
// hold no token, key or secret; secret fields carry obvious placeholders.
func Fixture(name string) []byte {
	b, err := fixtureFS.ReadFile("fixtures/" + name + ".json")
	if err != nil {
		panic(fmt.Sprintf("fake: no fixture %q", name))
	}
	return b
}

// fixtureMap decodes a fixture into a map the server can adjust.
func fixtureMap(name string) map[string]any {
	var m map[string]any
	if err := json.Unmarshal(Fixture(name), &m); err != nil {
		panic(err)
	}
	return m
}

// WorkflowJobQueuedEvent is a workflow_job webhook body (queued).
func WorkflowJobQueuedEvent() []byte { return Fixture("workflow_job_queued") }
