package hooks

import (
	"encoding/json"
	"os"
	"strings"
)

// AllowForkEnv set to 1 in the runner's environment says the repository's
// policy allows fork pull requests (github-integration R5.26). Absent or any
// other value means off.
const AllowForkEnv = "CRSP_ALLOW_FORK_PRS"

// ForkRefusal is the message a forked pull request job fails with. It names
// the policy, so the person who opened the pull request knows why.
const ForkRefusal = "This job was stopped before any step ran: it belongs to a pull request from a fork of a public repository, " +
	"and the crsp policy for this repository does not allow fork pull requests on the shared pool. " +
	"Ask the repository owner to allow fork pull requests in crsp, or run the job on a hosted runner."

// ForkPullRequest reports whether the job's event is a pull request from a
// fork of a public repository that the policy does not allow. It reads the
// event payload at GITHUB_EVENT_PATH. A payload that cannot be read or does not
// say whether the head is a fork counts as a fork, and a visibility that cannot
// be read counts as public, but only for a pull request event: any other event
// is never refused here.
func ForkPullRequest(getenv func(string) string) bool {
	name := getenv("GITHUB_EVENT_NAME")
	if name != "pull_request" && name != "pull_request_target" {
		return false
	}
	if strings.TrimSpace(getenv(AllowForkEnv)) == "1" {
		return false
	}
	var ev struct {
		Repository struct {
			Private *bool  `json:"private"`
			Name    string `json:"full_name"`
		} `json:"repository"`
		PullRequest struct {
			Head struct {
				Repo *struct {
					Fork *bool  `json:"fork"`
					Name string `json:"full_name"`
				} `json:"repo"`
			} `json:"head"`
		} `json:"pull_request"`
	}
	path := getenv("GITHUB_EVENT_PATH")
	if path == "" {
		return true
	}
	b, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(b, &ev) != nil {
		return true
	}
	if ev.Repository.Private != nil && *ev.Repository.Private {
		return false // a private repository: the fork rule is for public ones (R5.26)
	}
	head := ev.PullRequest.Head.Repo
	if head == nil {
		return true // a deleted fork has no head repository
	}
	if head.Fork != nil {
		return *head.Fork
	}
	base := ev.Repository.Name
	return base == "" || !strings.EqualFold(head.Name, base)
}
