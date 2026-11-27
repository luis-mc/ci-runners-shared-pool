package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/addresses"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// This file is crsp's own client of GitHub's runner scale-set protocol
// (github-integration R5.35, R5.40). It is written from the protocol's public
// description: register against the repository, find or make one scale set,
// open a message session, long-poll its queue, acquire the jobs that are
// offered, acknowledge each message. It is a preview API, so every answer that
// does not look as expected is an ErrScaleSetAPI and the demand falls back to
// polling (internal/demand Failover).

// ScaleSetAPIVersion is the one preview version the client speaks.
const ScaleSetAPIVersion = "6.0-preview"

// Errors of the scale-set client.
var (
	// ErrScaleSetAPI: the service answered with something the client does not
	// know. The preview API may have changed; callers fall back to polling.
	ErrScaleSetAPI = errors.New("github scale set: unexpected answer")
	// ErrScaleSetSession: the message session ended (expired, replaced or
	// refused). Callers open a new one after a back-off.
	ErrScaleSetSession = errors.New("github scale set: session ended")
)

// ScaleSetClient talks to one scale set of one repository or organization.
type ScaleSetClient struct {
	// APIBase is addresses.DefaultGitHubAPIURL unless a test or GHES says otherwise.
	APIBase string
	// ConfigURL is the web address of the scope: https://github.com/<owner>/<repo>.
	ConfigURL string
	// RegistrationToken returns a registration token of the scope. It is asked
	// for once per session and dropped.
	RegistrationToken func(ctx context.Context) (string, error)
	// Name is the scale set name, GroupID the runner group it lives in.
	Name    string
	GroupID int64
	Labels  []string
	HTTP    *http.Client
	// Owner is how the session names its owner (default: the host name).
	Owner string

	svcURL string
	admin  string // bearer of the admin calls; in memory only
	setID  int64
}

func (c *ScaleSetClient) client() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 70 * time.Second}
}

// do sends one JSON request and decodes the answer. Statuses 401 and 403 are
// a session problem; any other non-2xx is an unexpected answer. The body of an
// error answer is cut short and never holds a token (GitHub does not echo one).
func (c *ScaleSetClient) do(ctx context.Context, method, rawURL, auth string, body, out any) (int, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, rd)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", auth)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.client().Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		return 0, fmt.Errorf("%w: %v", ErrUnavailable, urlErrText(err))
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return res.StatusCode, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	switch {
	case res.StatusCode == http.StatusAccepted && method == http.MethodGet:
		return res.StatusCode, nil // no message yet
	case res.StatusCode == http.StatusUnauthorized, res.StatusCode == http.StatusForbidden, res.StatusCode == http.StatusNotFound && strings.Contains(rawURL, "/sessions"):
		return res.StatusCode, ErrScaleSetSession
	case res.StatusCode >= 500:
		return res.StatusCode, fmt.Errorf("%w: status %d", ErrUnavailable, res.StatusCode)
	case res.StatusCode >= 300:
		return res.StatusCode, fmt.Errorf("%w: status %d from %s %s", ErrScaleSetAPI, res.StatusCode, method, redactURL(rawURL))
	}
	if out != nil && len(bytes.TrimSpace(raw)) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return res.StatusCode, fmt.Errorf("%w: %v", ErrScaleSetAPI, err)
		}
	}
	return res.StatusCode, nil
}

func urlErrText(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Op + ": " + ue.Err.Error()
	}
	return err.Error()
}

// redactURL drops the query, which may name a session.
func redactURL(u string) string {
	if i := strings.IndexByte(u, '?'); i >= 0 {
		return u[:i]
	}
	return u
}

func (c *ScaleSetClient) api(path string, q url.Values) string {
	if q == nil {
		q = url.Values{}
	}
	q.Set("api-version", ScaleSetAPIVersion)
	return strings.TrimRight(c.svcURL, "/") + path + "?" + q.Encode()
}

// Connect registers against the scope, then finds the scale set by name or
// makes it. It keeps the admin bearer in memory only.
func (c *ScaleSetClient) Connect(ctx context.Context) error {
	if c.RegistrationToken == nil || c.Name == "" || c.ConfigURL == "" {
		return errors.New("github scale set: a registration source, a name and a scope address are needed")
	}
	tok, err := c.RegistrationToken(ctx)
	if err != nil {
		return err
	}
	base := c.APIBase
	if base == "" {
		base = addresses.DefaultGitHubAPIURL
	}
	var reg struct {
		URL   string `json:"url"`
		Token string `json:"token"`
	}
	if _, err := c.do(ctx, http.MethodPost, strings.TrimRight(base, "/")+"/actions/runner-registration", "RemoteAuth "+tok,
		map[string]string{"url": c.ConfigURL, "runner_event": "register"}, &reg); err != nil {
		return err
	}
	if reg.URL == "" || reg.Token == "" {
		return fmt.Errorf("%w: registration has no service address", ErrScaleSetAPI)
	}
	c.svcURL, c.admin = reg.URL, reg.Token

	var found struct {
		Value []struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
		} `json:"value"`
	}
	if _, err := c.do(ctx, http.MethodGet, c.api("/_apis/runtime/runnerscalesets", url.Values{"name": {c.Name}}), "Bearer "+c.admin, nil, &found); err != nil {
		return err
	}
	for _, s := range found.Value {
		if s.Name == c.Name {
			c.setID = s.ID
			return nil
		}
	}
	labels := []map[string]string{}
	for _, l := range c.Labels {
		labels = append(labels, map[string]string{"name": l, "type": "System"})
	}
	group := c.GroupID
	if group == 0 {
		group = 1
	}
	var made struct {
		ID int64 `json:"id"`
	}
	if _, err := c.do(ctx, http.MethodPost, c.api("/_apis/runtime/runnerscalesets", nil), "Bearer "+c.admin, map[string]any{
		"name": c.Name, "runnerGroupId": group, "labels": labels,
		"runnerSetting": map[string]any{"ephemeral": true, "isElastic": true, "disableUpdate": true},
	}, &made); err != nil {
		return err
	}
	if made.ID == 0 {
		return fmt.Errorf("%w: the new scale set has no id", ErrScaleSetAPI)
	}
	c.setID = made.ID
	return nil
}

// ScaleSetID is the id of the scale set after Connect.
func (c *ScaleSetClient) ScaleSetID() int64 { return c.setID }

// ScaleSetSession is one open message session.
type ScaleSetSession struct {
	c         *ScaleSetClient
	id        string
	queueURL  string
	queueAuth string
	lastID    int64
}

// OpenSession opens the message session of the scale set.
func (c *ScaleSetClient) OpenSession(ctx context.Context) (*ScaleSetSession, error) {
	if c.setID == 0 {
		return nil, errors.New("github scale set: connect first")
	}
	owner := c.Owner
	if owner == "" {
		owner, _ = os.Hostname()
	}
	var out struct {
		SessionID   string `json:"sessionId"`
		QueueURL    string `json:"messageQueueUrl"`
		QueueToken  string `json:"messageQueueAccessToken"`
		OwnerName   string `json:"ownerName"`
		Statistical any    `json:"statistics"`
	}
	path := "/_apis/runtime/runnerscalesets/" + strconv.FormatInt(c.setID, 10) + "/sessions"
	if _, err := c.do(ctx, http.MethodPost, c.api(path, nil), "Bearer "+c.admin, map[string]any{"ownerName": owner}, &out); err != nil {
		return nil, err
	}
	if out.SessionID == "" || out.QueueURL == "" || out.QueueToken == "" {
		return nil, fmt.Errorf("%w: the session has no queue", ErrScaleSetAPI)
	}
	return &ScaleSetSession{c: c, id: out.SessionID, queueURL: out.QueueURL, queueAuth: "Bearer " + out.QueueToken}, nil
}

// ScaleSetJob is one job event of a message.
type ScaleSetJob struct {
	Type       string // JobAvailable, JobAssigned, JobStarted or JobCompleted
	RequestID  int64
	Owner      string
	Repository string
	Event      string
	Labels     []string
	RunnerName string
	Result     string
}

// ScaleSetMessage is one message of the queue.
type ScaleSetMessage struct {
	ID            int64
	Jobs          []ScaleSetJob
	TotalAssigned int
}

// Next long-polls the queue for one message. It returns nil, nil when the
// service has none yet (the poll timed out); the caller asks again.
func (s *ScaleSetSession) Next(ctx context.Context) (*ScaleSetMessage, error) {
	q := url.Values{"sessionId": {s.id}, "lastMessageId": {strconv.FormatInt(s.lastID, 10)}}
	var raw struct {
		ID         int64  `json:"messageId"`
		Type       string `json:"messageType"`
		Body       string `json:"body"`
		Statistics struct {
			TotalAssignedJobs int `json:"totalAssignedJobs"`
		} `json:"statistics"`
	}
	code, err := s.c.do(ctx, http.MethodGet, s.queueURL+sep(s.queueURL)+q.Encode(), s.queueAuth, nil, &raw)
	if err != nil {
		return nil, err
	}
	if code == http.StatusAccepted {
		return nil, nil
	}
	if raw.ID == 0 {
		return nil, fmt.Errorf("%w: a message has no id", ErrScaleSetAPI)
	}
	if raw.Type != "RunnerScaleSetJobMessages" {
		// Unknown kind: acknowledge it and report nothing, so the queue moves on.
		s.lastID = raw.ID
		return &ScaleSetMessage{ID: raw.ID}, nil
	}
	var items []struct {
		Type       string   `json:"messageType"`
		RequestID  int64    `json:"runnerRequestId"`
		Owner      string   `json:"ownerName"`
		Repository string   `json:"repositoryName"`
		Event      string   `json:"eventName"`
		Labels     []string `json:"requestLabels"`
		RunnerName string   `json:"runnerName"`
		Result     string   `json:"result"`
	}
	if body := strings.TrimSpace(raw.Body); body != "" {
		if err := json.Unmarshal([]byte(body), &items); err != nil {
			return nil, fmt.Errorf("%w: message body: %v", ErrScaleSetAPI, err)
		}
	}
	m := &ScaleSetMessage{ID: raw.ID, TotalAssigned: raw.Statistics.TotalAssignedJobs}
	for _, it := range items {
		m.Jobs = append(m.Jobs, ScaleSetJob{Type: it.Type, RequestID: it.RequestID, Owner: it.Owner, Repository: it.Repository,
			Event: it.Event, Labels: it.Labels, RunnerName: it.RunnerName, Result: it.Result})
	}
	s.lastID = raw.ID
	return m, nil
}

func sep(u string) string {
	if strings.Contains(u, "?") {
		return "&"
	}
	return "?"
}

// Ack deletes a message from the queue once it was handled.
func (s *ScaleSetSession) Ack(ctx context.Context, messageID int64) error {
	u := strings.TrimRight(s.queueURL, "/")
	q := url.Values{"sessionId": {s.id}}
	if i := strings.IndexByte(u, '?'); i >= 0 {
		u = u[:i]
	}
	_, err := s.c.do(ctx, http.MethodDelete, u+"/"+strconv.FormatInt(messageID, 10)+"?"+q.Encode(), s.queueAuth, nil, nil)
	return err
}

// Acquire claims the offered jobs so no other listener takes them. It returns
// the request ids GitHub gave to this client.
func (s *ScaleSetSession) Acquire(ctx context.Context, requestIDs []int64) ([]int64, error) {
	if len(requestIDs) == 0 {
		return nil, nil
	}
	var out struct {
		Value []int64 `json:"value"`
	}
	path := "/_apis/runtime/runnerscalesets/" + strconv.FormatInt(s.c.setID, 10) + "/acquirejobs"
	if _, err := s.c.do(ctx, http.MethodPost, s.c.api(path, nil), "Bearer "+s.c.admin, requestIDs, &out); err != nil {
		return nil, err
	}
	return out.Value, nil
}

// Close deletes the session. A failure is not an error worth acting on.
func (s *ScaleSetSession) Close(ctx context.Context) error {
	path := "/_apis/runtime/runnerscalesets/" + strconv.FormatInt(s.c.setID, 10) + "/sessions/" + url.PathEscape(s.id)
	_, err := s.c.do(ctx, http.MethodDelete, s.c.api(path, nil), "Bearer "+s.c.admin, nil, nil)
	return err
}

// GenerateJITConfig asks the scale set for the JIT config of one runner. The
// runner of a scale set must come from here, not from the repository-level
// call, or the scale set would not give it its jobs. The value masks itself
// like every JITConfig (R5.34).
func (c *ScaleSetClient) GenerateJITConfig(ctx context.Context, name string) (JITConfig, error) {
	if c.setID == 0 {
		return JITConfig{}, errors.New("github scale set: connect first")
	}
	if name == "" {
		return JITConfig{}, errors.New("github scale set: a JIT config needs a runner name")
	}
	var out struct {
		Runner struct {
			ID int64 `json:"id"`
		} `json:"runner"`
		Encoded string `json:"encodedJITConfig"`
	}
	path := "/_apis/runtime/runnerscalesets/" + strconv.FormatInt(c.setID, 10) + "/generatejitconfig"
	if _, err := c.do(ctx, http.MethodPost, c.api(path, nil), "Bearer "+c.admin, map[string]any{"name": name, "workFolder": "_work"}, &out); err != nil {
		return JITConfig{}, err
	}
	if out.Encoded == "" {
		return JITConfig{}, fmt.Errorf("%w: no JIT config", ErrScaleSetAPI)
	}
	return NewJITConfig(out.Runner.ID, out.Encoded), nil
}
