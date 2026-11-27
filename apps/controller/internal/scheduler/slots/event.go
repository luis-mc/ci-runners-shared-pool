package slots

import (
	"encoding/json"
	"os"
)

// EventClass is 0 for a ready (not draft) pull request or a run on the
// repository's default branch, and 1 for anything else (R5.7;
// machine_slots.py priority_from_event). The class is read from the job's
// event payload at env["GITHUB_EVENT_PATH"].
func EventClass(getenv func(string) string) int {
	var event map[string]any
	if path := getenv("GITHUB_EVENT_PATH"); path != "" {
		if b, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(b, &event)
		}
	}
	return EventClassOf(event, getenv("GITHUB_REF"))
}

// EventClassOf classifies an already parsed event payload and ref.
func EventClassOf(event map[string]any, ref string) int {
	if pr, ok := event["pull_request"].(map[string]any); ok {
		if draft, _ := pr["draft"].(bool); draft {
			return 1
		}
		return 0
	}
	if repo, ok := event["repository"].(map[string]any); ok {
		if def, _ := repo["default_branch"].(string); def != "" && ref == "refs/heads/"+def {
			return 0
		}
	}
	return 1
}
