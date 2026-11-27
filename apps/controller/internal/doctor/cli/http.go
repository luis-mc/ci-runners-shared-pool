package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
)

// HTTPClient talks to the controller's API.
type HTTPClient struct {
	// BaseURL is the console address, such as http://127.0.0.1:8460.
	BaseURL string
	// Token is the CLI sign-in token, sent as a bearer token; empty sends none.
	Token string
	HTTP  *http.Client
}

var _ Client = (*HTTPClient)(nil)

func (c *HTTPClient) do(ctx context.Context, method, path string, body any, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.BaseURL, "/")+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("the controller at %s is not reachable: %w", c.BaseURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		var env gen.ErrorEnvelope
		_ = json.Unmarshal(data, &env)
		switch resp.StatusCode {
		case http.StatusUnauthorized:
			return errors.New("not signed in: sign in with the CLI first")
		case http.StatusForbidden:
			return errors.New("not allowed: starting a doctor run needs the admin role")
		}
		if env.Error.Message != "" {
			return fmt.Errorf("%s (%s)", env.Error.Message, env.Error.Code)
		}
		return fmt.Errorf("the controller answered %d", resp.StatusCode)
	}
	return json.Unmarshal(data, out)
}

// StartRun implements Client.
func (c *HTTPClient) StartRun(ctx context.Context, scope gen.DoctorScope) (gen.DoctorRun, error) {
	var run gen.DoctorRun
	err := c.do(ctx, http.MethodPost, "/api/v1/doctor/runs", scope, &run)
	return run, err
}

// GetRun implements Client.
func (c *HTTPClient) GetRun(ctx context.Context, id string) (gen.DoctorRun, error) {
	var run gen.DoctorRun
	err := c.do(ctx, http.MethodGet, "/api/v1/doctor/runs/"+url.PathEscape(id), nil, &run)
	return run, err
}

// ResolveRepositories implements Client by searching the repository list.
func (c *HTTPClient) ResolveRepositories(ctx context.Context, fullNames []string) ([]int64, error) {
	var ids []int64
	for _, full := range fullNames {
		_, name, ok := strings.Cut(full, "/")
		if !ok || name == "" {
			return nil, fmt.Errorf("--repo wants OWNER/NAME, not %q", full)
		}
		found := false
		cursor := ""
		for !found {
			q := url.Values{"q": {name}, "limit": {"100"}}
			if cursor != "" {
				q.Set("cursor", cursor)
			}
			var page gen.RepositoryPage
			if err := c.do(ctx, http.MethodGet, "/api/v1/github/repositories?"+q.Encode(), nil, &page); err != nil {
				return nil, err
			}
			for _, r := range page.Items {
				if strings.EqualFold(r.FullName, full) {
					ids, found = append(ids, r.Id), true
					break
				}
			}
			if page.NextCursor == nil || *page.NextCursor == "" {
				break
			}
			cursor = *page.NextCursor
		}
		if !found {
			return nil, fmt.Errorf("no repository %q among the installations", full)
		}
	}
	return ids, nil
}
