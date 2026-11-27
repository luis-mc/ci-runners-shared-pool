package github

import (
	"context"
	"errors"
	"net/url"
)

// ErrTreeTruncated: GitHub cut the recursive tree of a commit short, so the
// list of files is not whole and a hash over it would be wrong.
var ErrTreeTruncated = errors.New("github: the file tree of this commit is too large to list")

// TreeBlob is one file of a commit's tree: its path, blob SHA and size in bytes.
type TreeBlob struct {
	Path string
	SHA  string
	Size int64
}

// ListBlobsAt lists every regular file of a commit with one tree call
// (GET /repos/{owner}/{repo}/git/trees/{ref}?recursive=1), for the stack scan
// (IMG2, registry R5.73). Symbolic links and submodules are left out. The call
// goes through the shared REST client: it is counted against the installation's
// budget and refused for a background context when the budget is low (R6.6).
// A tree GitHub had to cut short gives ErrTreeTruncated.
func (c *REST) ListBlobsAt(ctx context.Context, installationID int64, owner, repo, ref string) ([]TreeBlob, error) {
	r, err := c.installationReq(ctx, installationID, req{method: "GET",
		path:  "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo) + "/git/trees/" + url.PathEscape(ref),
		query: url.Values{"recursive": {"1"}}})
	if err != nil {
		return nil, err
	}
	var t struct {
		Tree []struct {
			Path string `json:"path"`
			Mode string `json:"mode"`
			Type string `json:"type"`
			SHA  string `json:"sha"`
			Size int64  `json:"size"`
		} `json:"tree"`
		Truncated bool `json:"truncated"`
	}
	if _, err := c.do(ctx, r, &t); err != nil {
		return nil, err
	}
	if t.Truncated {
		return nil, ErrTreeTruncated
	}
	out := make([]TreeBlob, 0, len(t.Tree))
	for _, e := range t.Tree {
		if e.Type != "blob" || e.Mode == "120000" {
			continue
		}
		out = append(out, TreeBlob{Path: e.Path, SHA: e.SHA, Size: e.Size})
	}
	return out, nil
}
