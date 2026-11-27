package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Organization membership roles (GET /user/memberships/orgs/{org}).
const (
	OrgRoleOwner  = "admin"
	OrgRoleMember = "member"
)

// Runner group visibility. The controller only ever creates and keeps groups
// with VisibilitySelected, so a group never reaches a repository the owner did
// not select (github-integration R5.21).
const VisibilitySelected = "selected"

// RunnerGroup is an organization runner group.
type RunnerGroup struct {
	ID         int64
	Name       string
	Visibility string // all, selected or private
	Default    bool
}

func orgPath(org string) string { return "/orgs/" + url.PathEscape(org) }

// OrgMembership reads the signed-in user's membership of an organization with
// the user's own token, fresh. It returns the role ("admin" is an owner,
// "member" a member) and whether the membership is active. A user who is not
// in the organization gets ErrNotFound.
func (c *REST) OrgMembership(ctx context.Context, tok UserToken, org string) (role string, active bool, err error) {
	var out struct {
		Role  string `json:"role"`
		State string `json:"state"`
	}
	if _, err := c.do(ctx, req{method: "GET", path: "/user/memberships/orgs/" + url.PathEscape(org), bearer: tok.Value}, &out); err != nil {
		return "", false, err
	}
	return out.Role, out.State == "active", nil
}

// OrgMemberRole reads a user's membership of an organization as the App, for a
// caller that holds no GitHub user token, such as a CLI token. It answers the
// same question as OrgMembership.
func (c *REST) OrgMemberRole(ctx context.Context, installationID int64, org, login string) (role string, active bool, err error) {
	r, err := c.withInstallation(ctx, installationID, req{method: "GET",
		path: orgPath(org) + "/memberships/" + url.PathEscape(login)})
	if err != nil {
		return "", false, err
	}
	var out struct {
		Role  string `json:"role"`
		State string `json:"state"`
	}
	if _, err := c.do(ctx, r, &out); err != nil {
		return "", false, err
	}
	return out.Role, out.State == "active", nil
}

type apiGroup struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Visibility string `json:"visibility"`
	Default    bool   `json:"default"`
}

func (g apiGroup) group() RunnerGroup {
	return RunnerGroup(g)
}

// ListRunnerGroups lists the runner groups of an organization, all pages.
func (c *REST) ListRunnerGroups(ctx context.Context, installationID int64, org string) ([]RunnerGroup, error) {
	r, err := c.withInstallation(ctx, installationID, req{path: orgPath(org) + "/actions/runner-groups"})
	if err != nil {
		return nil, err
	}
	var all []RunnerGroup
	err = c.getAll(ctx, r, func(raw json.RawMessage) error {
		var page struct {
			Groups []apiGroup `json:"runner_groups"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			return err
		}
		for _, g := range page.Groups {
			all = append(all, g.group())
		}
		return nil
	})
	return all, err
}

// CreateRunnerGroup creates a runner group that is visible to the selected
// repositories only. The visibility is not a parameter: a group made here is
// never wider than repoIDs.
func (c *REST) CreateRunnerGroup(ctx context.Context, installationID int64, org, name string, repoIDs []int64) (RunnerGroup, error) {
	if strings.TrimSpace(name) == "" {
		return RunnerGroup{}, errors.New("github: a runner group needs a name")
	}
	if repoIDs == nil {
		repoIDs = []int64{}
	}
	r, err := c.withInstallation(ctx, installationID, req{method: "POST", write: true, path: orgPath(org) + "/actions/runner-groups",
		body: map[string]any{"name": name, "visibility": VisibilitySelected, "selected_repository_ids": repoIDs}})
	if err != nil {
		return RunnerGroup{}, err
	}
	var out apiGroup
	if _, err := c.do(ctx, r, &out); err != nil {
		return RunnerGroup{}, err
	}
	if out.ID == 0 {
		return RunnerGroup{}, errors.New("github: the runner group came back without an id")
	}
	return out.group(), nil
}

// SelectRunnerGroup sets a group's visibility to selected repositories, so a
// group that was wider is narrowed before its repositories are set.
func (c *REST) SelectRunnerGroup(ctx context.Context, installationID int64, org string, groupID int64) error {
	r, err := c.withInstallation(ctx, installationID, req{method: "PATCH", write: true,
		path: fmt.Sprintf("%s/actions/runner-groups/%d", orgPath(org), groupID), body: map[string]any{"visibility": VisibilitySelected}})
	if err != nil {
		return err
	}
	_, err = c.do(ctx, r, nil)
	return err
}

// ListRunnerGroupRepositories lists the ids of the repositories a selected
// group is visible to, all pages.
func (c *REST) ListRunnerGroupRepositories(ctx context.Context, installationID int64, org string, groupID int64) ([]int64, error) {
	r, err := c.withInstallation(ctx, installationID, req{path: fmt.Sprintf("%s/actions/runner-groups/%d/repositories", orgPath(org), groupID)})
	if err != nil {
		return nil, err
	}
	var ids []int64
	err = c.getAll(ctx, r, func(raw json.RawMessage) error {
		var page struct {
			Repositories []struct {
				ID int64 `json:"id"`
			} `json:"repositories"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			return err
		}
		for _, x := range page.Repositories {
			ids = append(ids, x.ID)
		}
		return nil
	})
	return ids, err
}

// SetRunnerGroupRepositories replaces the repositories of a selected group with
// repoIDs (PUT .../repositories).
func (c *REST) SetRunnerGroupRepositories(ctx context.Context, installationID int64, org string, groupID int64, repoIDs []int64) error {
	if repoIDs == nil {
		repoIDs = []int64{}
	}
	r, err := c.withInstallation(ctx, installationID, req{method: "PUT", write: true,
		path: fmt.Sprintf("%s/actions/runner-groups/%d/repositories", orgPath(org), groupID),
		body: map[string]any{"selected_repository_ids": repoIDs}})
	if err != nil {
		return err
	}
	_, err = c.do(ctx, r, nil)
	return err
}
