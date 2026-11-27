package github

import (
	"context"
	"errors"
	"fmt"
)

// JITRequest asks for the just-in-time config of one ephemeral runner (R5.34).
type JITRequest struct {
	Name string
	// GroupID is the runner group. A repository runner uses the default group,
	// id 1, when it is zero.
	GroupID    int64
	Labels     []string
	WorkFolder string
}

// JITConfig is what GitHub gives back. The encoded config is a secret: it is
// handed to the agent and dropped, never stored or logged (R5.34). Printing
// the value in any form shows a mask.
type JITConfig struct {
	RunnerID int64
	encoded  string
}

// NewJITConfig wraps an encoded config.
func NewJITConfig(runnerID int64, encoded string) JITConfig {
	return JITConfig{RunnerID: runnerID, encoded: encoded}
}

// Encoded returns the secret text. Call it only to pass it to the agent link.
func (j JITConfig) Encoded() string { return j.encoded }

// String masks the secret in %s and %v.
func (j JITConfig) String() string { return fmt.Sprintf("jitconfig(runner %d, ***)", j.RunnerID) }

// GoString masks the secret in %#v.
func (j JITConfig) GoString() string { return j.String() }

// MarshalJSON masks the secret, so a log of the value never carries it.
func (j JITConfig) MarshalJSON() ([]byte, error) {
	return []byte(fmt.Sprintf(`{"runner_id":%d,"encoded":"***"}`, j.RunnerID)), nil
}

// GenerateJITConfig implements InstallationClient (R5.34). It calls
// generate-jitconfig at repository or organization level. The result is not
// logged, and nothing here keeps it.
func (c *REST) GenerateJITConfig(ctx context.Context, id int64, scope RunnerScope, in JITRequest) (JITConfig, error) {
	if in.Name == "" {
		return JITConfig{}, errors.New("github: a JIT config needs a runner name")
	}
	group := in.GroupID
	if group == 0 {
		group = scope.GroupID
	}
	if group == 0 {
		group = 1
	}
	labels := in.Labels
	if labels == nil {
		labels = []string{}
	}
	body := map[string]any{"name": in.Name, "runner_group_id": group, "labels": labels}
	if in.WorkFolder != "" {
		body["work_folder"] = in.WorkFolder
	}
	r, err := c.installationReq(ctx, id, req{method: "POST", path: scopePath(scope) + "/actions/runners/generate-jitconfig", write: true, body: body})
	if err != nil {
		return JITConfig{}, err
	}
	var out struct {
		Runner struct {
			ID int64 `json:"id"`
		} `json:"runner"`
		Encoded string `json:"encoded_jit_config"`
	}
	if _, err := c.do(ctx, r, &out); err != nil {
		return JITConfig{}, err
	}
	if out.Encoded == "" {
		return JITConfig{}, errors.New("github: empty JIT config")
	}
	return NewJITConfig(out.Runner.ID, out.Encoded), nil
}
