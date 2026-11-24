package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
)

// RequiredPermissions is the exact permission set the App is made with
// (R5.23), as GitHub names them in a manifest and in an installation.
var RequiredPermissions = map[string]string{
	"administration":                   "write",
	"actions":                          "read",
	"actions_variables":                "write",
	"contents":                         "read",
	"metadata":                         "read",
	"organization_self_hosted_runners": "write",
}

var accessRank = map[string]int{"read": 1, "write": 2, "admin": 3}

// MissingPermissions compares an installation's granted permissions with
// RequiredPermissions and names each one that is missing or too weak, sorted
// (R5.24).
func MissingPermissions(granted map[string]string) []string {
	var missing []string
	for name, need := range RequiredPermissions {
		if accessRank[granted[name]] < accessRank[need] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	return missing
}

// MissingPermissionsOf is MissingPermissions for the named permissions only.
// It returns nil when each one is granted at the level R5.23 asks for.
func MissingPermissionsOf(granted map[string]string, names ...string) []string {
	var missing []string
	for _, n := range names {
		if accessRank[granted[n]] < accessRank[RequiredPermissions[n]] {
			missing = append(missing, n)
		}
	}
	return missing
}

// ListAppInstallations lists every installation of the App, with the App's
// JWT (R5.22).
func (c *REST) ListAppInstallations(ctx context.Context) ([]Installation, error) {
	jwt, err := c.appJWT(ctx)
	if err != nil {
		return nil, err
	}
	var all []Installation
	err = c.getAll(ctx, req{path: "/app/installations", bearer: jwt}, func(raw json.RawMessage) error {
		var page []apiInstallation
		if err := json.Unmarshal(raw, &page); err != nil {
			return err
		}
		for _, i := range page {
			all = append(all, i.install())
		}
		return nil
	})
	return all, err
}

// ListAppRepositories lists the repositories an installation can reach, with
// an installation token (R5.22). Admin is not set: no user is involved.
func (c *REST) ListAppRepositories(ctx context.Context, installationID int64) ([]Repository, error) {
	r, err := c.withInstallation(ctx, installationID, req{path: "/installation/repositories"})
	if err != nil {
		return nil, err
	}
	var all []Repository
	err = c.getAll(ctx, r, func(raw json.RawMessage) error {
		var page struct {
			Repositories []struct {
				ID            int64  `json:"id"`
				FullName      string `json:"full_name"`
				Name          string `json:"name"`
				Private       bool   `json:"private"`
				Visibility    string `json:"visibility"`
				DefaultBranch string `json:"default_branch"`
				Archived      bool   `json:"archived"`
				Owner         struct {
					Login string `json:"login"`
				} `json:"owner"`
			} `json:"repositories"`
		}
		if err := json.Unmarshal(raw, &page); err != nil {
			return err
		}
		for _, x := range page.Repositories {
			vis := x.Visibility
			if vis == "" {
				vis = "public"
				if x.Private {
					vis = "private"
				}
			}
			all = append(all, Repository{ID: x.ID, FullName: x.FullName, Owner: x.Owner.Login, Name: x.Name,
				Visibility: vis, DefaultBranch: x.DefaultBranch, Archived: x.Archived})
		}
		return nil
	})
	return all, err
}

// CollaboratorPermission reads a user's permission on a repository as the
// App (GET .../collaborators/{login}/permission). It is the fresh admin check
// for callers that hold no GitHub user token, such as a CLI token (R5.19).
func (c *REST) CollaboratorPermission(ctx context.Context, installationID int64, owner, repo, login string) (string, error) {
	r, err := c.withInstallation(ctx, installationID, req{path: fmt.Sprintf("/repos/%s/%s/collaborators/%s/permission",
		url.PathEscape(owner), url.PathEscape(repo), url.PathEscape(login))})
	if err != nil {
		return "", err
	}
	var out struct {
		Permission string `json:"permission"`
	}
	r.method = "GET"
	if _, err := c.do(ctx, r, &out); err != nil {
		return "", err
	}
	return out.Permission, nil
}

// ForkApprovalAllOutside is the fork pull request approval policy that holds
// every outside contributor's workflow runs for approval (R5.26).
const ForkApprovalAllOutside = "all_external_contributors"

// SetForkApproval sets the repository's fork pull request approval policy
// (PUT .../actions/permissions/fork-pr-contributor-approval).
func (c *REST) SetForkApproval(ctx context.Context, installationID int64, owner, repo, policy string) error {
	r, err := c.withInstallation(ctx, installationID, req{method: "PUT", write: true,
		path: fmt.Sprintf("/repos/%s/%s/actions/permissions/fork-pr-contributor-approval", url.PathEscape(owner), url.PathEscape(repo)),
		body: map[string]string{"approval_policy": policy}})
	if err != nil {
		return err
	}
	_, err = c.do(ctx, r, nil)
	return err
}
