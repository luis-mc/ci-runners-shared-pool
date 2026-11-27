package fake

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// The routes of OPS73: custom labels of a runner, and the one pull request
// that changes workflow files. State is kept on the fake so a test can assert
// on what the code did.

// FakePR is a pull request the fake holds.
type FakePR struct {
	Number int
	Title  string
	Body   string
	Head   string
	Base   string
	Open   bool
}

// routingState is the per-fake state of these routes.
type routingState struct {
	runnerLabels map[string][]string          // "scope/runner id" -> custom labels
	branches     map[string]map[string]string // full repo name -> branch -> sha
	branchFiles  map[string]map[string]string // "full@branch" -> path -> content
	prs          map[string][]*FakePR         // full repo name -> pull requests
}

func (s *Server) rs() *routingState {
	if s.routing == nil {
		s.routing = &routingState{runnerLabels: map[string][]string{}, branches: map[string]map[string]string{},
			branchFiles: map[string]map[string]string{}, prs: map[string][]*FakePR{}}
	}
	return s.routing
}

// RunnerCustomLabels lists the custom labels the fake holds for a runner.
func (s *Server) RunnerCustomLabels(scope string, runnerID int64) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.rs().runnerLabels[fmt.Sprintf("%s/%d", scope, runnerID)]...)
}

// SetBranch sets the head of a branch.
func (s *Server) SetBranch(full, branch, sha string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.rs()
	if st.branches[full] == nil {
		st.branches[full] = map[string]string{}
	}
	st.branches[full][branch] = sha
}

// PullRequestsOf lists the pull requests of a repository.
func (s *Server) PullRequestsOf(full string) []FakePR {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []FakePR
	for _, p := range s.rs().prs[full] {
		out = append(out, *p)
	}
	return out
}

// BranchFile reads a file as written on a branch by the pull request route.
func (s *Server) BranchFile(full, branch, path string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.rs().branchFiles[full+"@"+branch][path]
	return c, ok
}

func (s *Server) runnerLabelRoute(w http.ResponseWriter, r *http.Request, scope, rest string) bool {
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) < 2 || parts[1] != "labels" {
		return false
	}
	if _, err := strconv.ParseInt(parts[0], 10, 64); err != nil {
		return false
	}
	key := scope + "/" + parts[0]
	st := s.rs()
	switch {
	case len(parts) == 2 && r.Method == "POST":
		var in struct {
			Labels []string `json:"labels"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		if len(st.runnerLabels[key])+len(in.Labels) > 100 {
			writeJSON(w, 500, map[string]string{"message": "Server Error"}) // GitHub fails the 101st label with 500
			return true
		}
		st.runnerLabels[key] = append(st.runnerLabels[key], in.Labels...)
		writeJSON(w, 200, map[string]any{"total_count": len(st.runnerLabels[key])})
	case len(parts) == 3 && r.Method == "DELETE":
		var keep []string
		found := false
		for _, l := range st.runnerLabels[key] {
			if l == parts[2] {
				found = true
				continue
			}
			keep = append(keep, l)
		}
		if !found {
			notFound(w)
			return true
		}
		st.runnerLabels[key] = keep
		writeJSON(w, 200, map[string]any{"total_count": len(keep)})
	default:
		return false
	}
	return true
}

// routingRoute handles git refs, contents writes and pull requests; it says
// whether it took the request.
func (s *Server) routingRoute(w http.ResponseWriter, r *http.Request, full, rest string) bool {
	st := s.rs()
	switch {
	case strings.HasPrefix(rest, "git/ref/heads/") && r.Method == "GET":
		sha, ok := st.branches[full][strings.TrimPrefix(rest, "git/ref/heads/")]
		if !ok {
			notFound(w)
			return true
		}
		writeJSON(w, 200, map[string]any{"object": map[string]string{"sha": sha}})
	case rest == "git/refs" && r.Method == "POST":
		var in struct{ Ref, SHA string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		if st.branches[full] == nil {
			st.branches[full] = map[string]string{}
		}
		st.branches[full][strings.TrimPrefix(in.Ref, "refs/heads/")] = in.SHA
		w.WriteHeader(201)
	case strings.HasPrefix(rest, "contents/") && r.Method == "PUT":
		var in struct{ Message, Branch, Content, SHA string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		raw, err := base64.StdEncoding.DecodeString(in.Content)
		if err != nil {
			writeJSON(w, 422, map[string]string{"message": "bad content"})
			return true
		}
		k := full + "@" + in.Branch
		if st.branchFiles[k] == nil {
			st.branchFiles[k] = map[string]string{}
		}
		st.branchFiles[k][strings.TrimPrefix(rest, "contents/")] = string(raw)
		writeJSON(w, 200, map[string]any{"content": map[string]string{"sha": "newsha"}})
	case strings.HasPrefix(rest, "contents/") && r.Method == "GET" && st.branchFiles[full+"@"+r.URL.Query().Get("ref")] != nil:
		// A branch a pull request wrote to answers with the sha of its files;
		// every other read is the ordinary workflow file route.
		if _, ok := st.branchFiles[full+"@"+r.URL.Query().Get("ref")][strings.TrimPrefix(rest, "contents/")]; !ok {
			notFound(w)
			return true
		}
		writeJSON(w, 200, map[string]string{"sha": "oldsha"})
	case rest == "pulls" && r.Method == "GET":
		head := strings.TrimPrefix(r.URL.Query().Get("head"), strings.Split(full, "/")[0]+":")
		items := []map[string]any{}
		for _, p := range st.prs[full] {
			if p.Open && p.Head == head {
				items = append(items, map[string]any{"number": p.Number, "html_url": fmt.Sprintf("https://github.example/%s/pull/%d", full, p.Number)})
			}
		}
		writeJSON(w, 200, items)
	case rest == "pulls" && r.Method == "POST":
		var in struct{ Title, Body, Head, Base string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		p := &FakePR{Number: len(st.prs[full]) + 1, Title: in.Title, Body: in.Body, Head: in.Head, Base: in.Base, Open: true}
		st.prs[full] = append(st.prs[full], p)
		writeJSON(w, 201, map[string]any{"number": p.Number, "html_url": fmt.Sprintf("https://github.example/%s/pull/%d", full, p.Number)})
	default:
		return false
	}
	return true
}
