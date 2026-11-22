package fake

import (
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // git blob ids are SHA-1 by definition
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/nacl/box"
)

// route runs with s.mu held.
func (s *Server) route(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	switch {
	case r.Method == "POST" && strings.HasPrefix(p, "/app-manifests/") && strings.HasSuffix(p, "/conversions"):
		s.convert(w, strings.TrimSuffix(strings.TrimPrefix(p, "/app-manifests/"), "/conversions"))
	case r.Method == "POST" && p == "/login/oauth/access_token":
		s.oauthToken(w, r)
	case r.Method == "POST" && p == "/login/device/code":
		var in map[string]string
		_ = json.NewDecoder(r.Body).Decode(&in)
		s.nextID++
		code := fmt.Sprintf("FAKEDEVICE%d", s.nextID)
		s.DeviceCodes[code] = 0
		writeJSON(w, 200, map[string]any{"device_code": code, "user_code": "ABCD-1234",
			"verification_uri": s.URL + "/login/device", "expires_in": 900, "interval": 5})
	case r.Method == "POST" && strings.HasPrefix(p, "/applications/") && strings.HasSuffix(p, "/token"):
		s.checkToken(w, r)
	case r.Method == "GET" && p == "/app":
		if !s.checkJWT(bearer(r)) {
			unauthorized(w)
			return
		}
		app := fixtureMap("app_conversion")
		delete(app, "client_secret")
		delete(app, "webhook_secret")
		delete(app, "pem")
		if s.appPerms != nil {
			app["permissions"] = s.appPerms
		}
		if s.AppEvents != nil {
			app["events"] = s.AppEvents
		}
		writeJSON(w, 200, app)
	case r.Method == "PATCH" && p == "/app/hook/config":
		if !s.checkJWT(bearer(r)) {
			unauthorized(w)
			return
		}
		in := map[string]string{}
		_ = json.NewDecoder(r.Body).Decode(&in)
		s.Hook = in
		s.HookPatches++
		writeJSON(w, 200, map[string]any{"url": in["url"], "content_type": in["content_type"], "insecure_ssl": in["insecure_ssl"]})
	case r.Method == "GET" && p == "/app/hook/deliveries":
		if !s.checkJWT(bearer(r)) {
			unauthorized(w)
			return
		}
		items := []map[string]any{}
		for _, d := range s.Deliveries {
			status := "OK"
			if d.StatusCode < 200 || d.StatusCode > 299 {
				status = fmt.Sprintf("Invalid HTTP Response: %d", d.StatusCode)
			}
			items = append(items, map[string]any{"id": d.ID, "delivered_at": d.DeliveredAt.UTC().Format(time.RFC3339),
				"event": d.Event, "status": status, "status_code": d.StatusCode})
		}
		writeJSON(w, 200, items)
	case r.Method == "GET" && strings.HasPrefix(p, "/users/"):
		if !s.checkJWT(bearer(r)) {
			unauthorized(w)
			return
		}
		login := strings.TrimPrefix(p, "/users/")
		for _, u := range s.Users {
			if strings.EqualFold(u.Login, login) {
				writeJSON(w, 200, map[string]any{"id": u.ID, "login": u.Login})
				return
			}
		}
		notFound(w)
	case r.Method == "GET" && p == "/user":
		u, ok := s.userFor(r)
		if !ok {
			unauthorized(w)
			return
		}
		writeJSON(w, 200, map[string]any{"id": u.ID, "login": u.Login})
	case r.Method == "GET" && strings.HasPrefix(p, "/user/memberships/orgs/"):
		u, ok := s.userFor(r)
		if !ok {
			unauthorized(w)
			return
		}
		s.membership(w, strings.TrimPrefix(p, "/user/memberships/orgs/"), u.Login)
	case r.Method == "GET" && p == "/user/installations":
		s.userInstallations(w, r)
	case r.Method == "GET" && strings.HasPrefix(p, "/user/installations/") && strings.HasSuffix(p, "/repositories"):
		s.userRepositories(w, r)
	case r.Method == "GET" && p == "/app/installations":
		if !s.checkJWT(bearer(r)) {
			unauthorized(w)
			return
		}
		ids := make([]int64, 0, len(s.Installs))
		for id := range s.Installs {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		items := []map[string]any{}
		for _, id := range ids {
			items = append(items, s.installJSON(s.Installs[id]))
		}
		writeJSON(w, 200, items)
	case r.Method == "GET" && p == "/installation/repositories":
		id, ok := s.installFor(r)
		if !ok {
			unauthorized(w)
			return
		}
		var repos []*Repo
		for _, rp := range s.Repos {
			if rp.Installation == id {
				repos = append(repos, rp)
			}
		}
		sort.Slice(repos, func(i, j int) bool { return repos[i].ID < repos[j].ID })
		from, to, link := page(r, len(repos))
		items := []map[string]any{}
		for _, rp := range repos[from:to] {
			items = append(items, s.repoJSON(rp, 0))
		}
		if link != "" {
			w.Header().Set("Link", link)
		}
		writeJSON(w, 200, map[string]any{"total_count": len(repos), "repositories": items})
	case strings.HasPrefix(p, "/app/installations/"):
		s.appInstallations(w, r)
	case strings.HasPrefix(p, "/repos/"):
		s.repoRoutes(w, r)
	case strings.HasPrefix(p, "/orgs/"):
		s.orgRoutes(w, r)
	default:
		notFound(w)
	}
}

func (s *Server) convert(w http.ResponseWriter, code string) {
	if !s.ManifestCodes[code] {
		notFound(w)
		return
	}
	delete(s.ManifestCodes, code) // single use
	m := fixtureMap("app_conversion")
	m["id"] = s.AppID
	m["pem"] = s.PEM
	m["client_secret"] = s.ClientSecret
	if s.NoWebhookSecret {
		m["webhook_secret"] = ""
	}
	writeJSON(w, 201, m)
}

func (s *Server) oauthToken(w http.ResponseWriter, r *http.Request) {
	var in map[string]string
	_ = json.NewDecoder(r.Body).Decode(&in)
	if dc := in["device_code"]; dc != "" {
		s.deviceToken(w, dc)
		return
	}
	uid, ok := s.OAuthCodes[in["code"]]
	if !ok || in["client_secret"] != s.ClientSecret {
		writeJSON(w, 200, map[string]string{"error": "bad_verification_code"})
		return
	}
	// PKCE: S256(verifier) must equal the challenge bound to the code.
	sum := sha256.Sum256([]byte(in["code_verifier"]))
	if base64.RawURLEncoding.EncodeToString(sum[:]) != s.OAuthVerifiers[in["code"]] {
		writeJSON(w, 200, map[string]string{"error": "bad_verification_code"})
		return
	}
	delete(s.OAuthCodes, in["code"])
	tok := "ghu_" + "FAKE" + strconv.FormatInt(uid, 10) + strings.Repeat("y", 20)
	s.UserTokens[tok] = uid
	writeJSON(w, 200, map[string]any{"access_token": tok, "expires_in": 28800, "token_type": "bearer"})
}

func (s *Server) installJSON(in *Install) map[string]any {
	m := map[string]any{"id": in.ID, "repository_selection": in.Selection, "permissions": in.Perms,
		"account": map[string]any{"login": in.Account, "type": in.Type}, "suspended_at": nil}
	if in.Suspended {
		m["suspended_at"] = "2026-11-01T00:00:00Z"
	}
	return m
}

func (s *Server) userInstallations(w http.ResponseWriter, r *http.Request) {
	u, ok := s.userFor(r)
	if !ok {
		unauthorized(w)
		return
	}
	var ids []int64
	for id, in := range s.Installs {
		if in.Users[u.ID] {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	from, to, link := page(r, len(ids))
	items := []map[string]any{}
	for _, id := range ids[from:to] {
		items = append(items, s.installJSON(s.Installs[id]))
	}
	if link != "" {
		w.Header().Set("Link", link)
	}
	writeJSON(w, 200, map[string]any{"total_count": len(ids), "installations": items})
}

func (s *Server) repoJSON(rp *Repo, adminFor int64) map[string]any {
	vis := "public"
	if rp.Private {
		vis = "private"
	}
	return map[string]any{"id": rp.ID, "name": rp.Name, "full_name": rp.FullName(), "private": rp.Private,
		"visibility": vis, "default_branch": rp.DefaultBranch, "archived": rp.Archived,
		"owner":       map[string]any{"login": rp.Owner},
		"permissions": map[string]any{"admin": rp.Admins[adminFor], "push": true, "pull": true}}
}

func (s *Server) userRepositories(w http.ResponseWriter, r *http.Request) {
	u, ok := s.userFor(r)
	if !ok {
		unauthorized(w)
		return
	}
	idStr := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/user/installations/"), "/repositories")
	id, _ := strconv.ParseInt(idStr, 10, 64)
	in := s.Installs[id]
	if in == nil || !in.Users[u.ID] {
		notFound(w)
		return
	}
	var repos []*Repo
	for _, rp := range s.Repos {
		if rp.Installation == id {
			repos = append(repos, rp)
		}
	}
	sort.Slice(repos, func(i, j int) bool { return repos[i].ID < repos[j].ID })
	from, to, link := page(r, len(repos))
	items := []map[string]any{}
	for _, rp := range repos[from:to] {
		items = append(items, s.repoJSON(rp, u.ID))
	}
	if link != "" {
		w.Header().Set("Link", link)
	}
	writeJSON(w, 200, map[string]any{"total_count": len(repos), "repositories": items})
}

func (s *Server) appInstallations(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/app/installations/")
	if !s.checkJWT(bearer(r)) {
		unauthorized(w)
		return
	}
	if idStr, ok := strings.CutSuffix(rest, "/access_tokens"); ok && r.Method == "POST" {
		id, _ := strconv.ParseInt(idStr, 10, 64)
		if s.Installs[id] == nil {
			notFound(w)
			return
		}
		// Like GitHub, a request for permissions the installation lacks fails as a
		// whole with 422 (CRSP148); the body of each request is kept for tests.
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		s.tokenReqs = append(s.tokenReqs, string(raw))
		var ask struct {
			Permissions map[string]string `json:"permissions"`
		}
		if len(raw) > 0 && json.Unmarshal(raw, &ask) == nil {
			for name, level := range ask.Permissions {
				have := s.Installs[id].Perms[name]
				if have != level && (level != "read" || have != "write") {
					writeJSON(w, 422, map[string]any{"message": "The permissions requested are not granted to this installation."})
					return
				}
			}
		}
		s.nextID++
		tok := "ghs_" + "FAKE" + strconv.FormatInt(s.nextID, 10) + strings.Repeat("z", 20)
		s.instTokens[tok] = id
		s.tokensMade++
		writeJSON(w, 201, map[string]any{"token": tok, "expires_at": s.Now().Add(time.Hour).UTC().Format(time.RFC3339)})
		return
	}
	id, _ := strconv.ParseInt(rest, 10, 64)
	in := s.Installs[id]
	if in == nil {
		notFound(w)
		return
	}
	writeJSON(w, 200, s.installJSON(in))
}

// authInstall checks the installation token covers the repository.
func (s *Server) authInstall(w http.ResponseWriter, r *http.Request, rp *Repo) bool {
	id, ok := s.installFor(r)
	if !ok {
		// A user token may read a repository it can see (admin check).
		if u, uok := s.userFor(r); uok && rp != nil && r.Method == "GET" && r.URL.Path == "/repos/"+rp.Owner+"/"+rp.Name {
			_ = u
			return true
		}
		unauthorized(w)
		return false
	}
	if rp != nil && rp.Installation != id {
		notFound(w)
		return false
	}
	return true
}

func (s *Server) repoRoutes(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/repos/"), "/")
	if len(parts) < 2 {
		notFound(w)
		return
	}
	rp := s.repoByName(parts[0], parts[1])
	if rp == nil {
		notFound(w)
		return
	}
	if !s.authInstall(w, r, rp) {
		return
	}
	rest := strings.Join(parts[2:], "/")
	full := rp.FullName()
	switch {
	case rest == "" && r.Method == "GET":
		uid := int64(0)
		if u, ok := s.userFor(r); ok {
			uid = u.ID
		}
		writeJSON(w, 200, s.repoJSON(rp, uid))
	case strings.HasPrefix(rest, "actions/runners") && s.runnerLabelRoute(w, r, full, strings.TrimPrefix(rest, "actions/runners")):
	case s.routingRoute(w, r, full, rest):
	case strings.HasPrefix(rest, "actions/runners"):
		s.runnerRoutes(w, r, full, strings.TrimPrefix(rest, "actions/runners"))
	case rest == "actions/variables" || strings.HasPrefix(rest, "actions/variables/"):
		s.variableRoutes(w, r, full, strings.TrimPrefix(strings.TrimPrefix(rest, "actions/variables"), "/"))
	case rest == "actions/secrets/public-key" && r.Method == "GET":
		s.secretPublicKey(w, full)
	case strings.HasPrefix(rest, "actions/secrets/") && r.Method == "PUT":
		s.putSecret(w, r, full, strings.TrimPrefix(rest, "actions/secrets/"))
	case strings.HasPrefix(rest, "git/trees/"):
		s.gitTree(w, full)
	case strings.HasPrefix(rest, "contents/"):
		s.contents(w, r, full, strings.TrimPrefix(rest, "contents/"))
	case strings.HasPrefix(rest, "collaborators/") && strings.HasSuffix(rest, "/permission"):
		login := strings.TrimSuffix(strings.TrimPrefix(rest, "collaborators/"), "/permission")
		for _, u := range s.Users {
			if strings.EqualFold(u.Login, login) {
				perm := "write"
				if rp.Admins[u.ID] {
					perm = "admin"
				}
				writeJSON(w, 200, map[string]any{"permission": perm})
				return
			}
		}
		notFound(w)
	case rest == "actions/permissions/fork-pr-contributor-approval" && r.Method == "PUT":
		var in map[string]string
		_ = json.NewDecoder(r.Body).Decode(&in)
		s.ForkApproval[full] = in["approval_policy"]
		w.WriteHeader(204)
	case rest == "actions/runs" && r.Method == "GET":
		s.runs(w, r, full)
	case strings.HasPrefix(rest, "actions/runs/") && strings.HasSuffix(rest, "/jobs"):
		s.runJobs(w, r, full, rest)
	case strings.HasPrefix(rest, "actions/runs/") && !strings.Contains(strings.TrimPrefix(rest, "actions/runs/"), "/"):
		// One run: its workflow file is the one the fake serves first (ci.yml), at the branch.
		id, _ := strconv.ParseInt(strings.TrimPrefix(rest, "actions/runs/"), 10, 64)
		writeJSON(w, 200, map[string]any{"id": id, "path": ".github/workflows/ci.yml@refs/heads/main"})
	case strings.HasPrefix(rest, "actions/jobs/"):
		id, _ := strconv.ParseInt(strings.TrimPrefix(rest, "actions/jobs/"), 10, 64)
		for _, j := range s.Jobs {
			if j.ID == id && j.Repo == full {
				writeJSON(w, 200, jobJSON(j))
				return
			}
		}
		notFound(w)
	default:
		notFound(w)
	}
}

func (s *Server) orgRoutes(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/orgs/"), "/")
	if _, ok := s.installFor(r); !ok {
		unauthorized(w)
		return
	}
	rest := strings.Join(parts[1:], "/")
	if strings.HasPrefix(rest, "actions/runners") {
		s.runnerRoutes(w, r, parts[0], strings.TrimPrefix(rest, "actions/runners"))
		return
	}
	if strings.HasPrefix(rest, "memberships/") && r.Method == "GET" {
		s.membership(w, parts[0], strings.TrimPrefix(rest, "memberships/"))
		return
	}
	if rest == "actions/runner-groups" || strings.HasPrefix(rest, "actions/runner-groups/") {
		s.groupRoutes(w, r, parts[0], strings.TrimPrefix(strings.TrimPrefix(rest, "actions/runner-groups"), "/"))
		return
	}
	notFound(w)
}

// membership answers a membership read; an unknown member is a 404, as GitHub
// answers for a user who is not in the organization.
func (s *Server) membership(w http.ResponseWriter, org, login string) {
	role, ok := s.OrgMembers[strings.ToLower(org)][strings.ToLower(login)]
	if !ok {
		notFound(w)
		return
	}
	writeJSON(w, 200, map[string]any{"role": role, "state": "active"})
}

func (s *Server) groupRoutes(w http.ResponseWriter, r *http.Request, org, rest string) {
	o := strings.ToLower(org)
	if _, known := s.Groups[o]; !known {
		notFound(w)
		return
	}
	if rest == "" {
		switch r.Method {
		case "GET":
			items := []map[string]any{}
			for _, g := range s.Groups[o] {
				items = append(items, map[string]any{"id": g.ID, "name": g.Name, "visibility": g.Visibility, "default": g.Default})
			}
			writeJSON(w, 200, map[string]any{"total_count": len(items), "runner_groups": items})
		case "POST":
			var in struct {
				Name       string  `json:"name"`
				Visibility string  `json:"visibility"`
				Repos      []int64 `json:"selected_repository_ids"`
			}
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Name == "" {
				writeJSON(w, 422, map[string]any{"message": "Validation Failed"})
				return
			}
			for _, g := range s.Groups[o] {
				if strings.EqualFold(g.Name, in.Name) {
					writeJSON(w, 409, map[string]any{"message": "Runner group name already exists"})
					return
				}
			}
			if in.Visibility == "" {
				in.Visibility = "all"
			}
			s.nextID++
			g := &Group{ID: s.nextID, Name: in.Name, Visibility: in.Visibility, Repos: in.Repos}
			s.Groups[o] = append(s.Groups[o], g)
			writeJSON(w, 201, map[string]any{"id": g.ID, "name": g.Name, "visibility": g.Visibility, "default": false})
		default:
			notFound(w)
		}
		return
	}
	idText, sub, _ := strings.Cut(rest, "/")
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil {
		notFound(w)
		return
	}
	var g *Group
	for _, x := range s.Groups[o] {
		if x.ID == id {
			g = x
		}
	}
	if g == nil {
		notFound(w)
		return
	}
	switch {
	case sub == "" && r.Method == "PATCH":
		var in struct {
			Visibility string `json:"visibility"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, 422, map[string]any{"message": "Validation Failed"})
			return
		}
		if in.Visibility != "" {
			g.Visibility = in.Visibility
		}
		writeJSON(w, 200, map[string]any{"id": g.ID, "name": g.Name, "visibility": g.Visibility, "default": g.Default})
	case sub == "repositories" && r.Method == "GET":
		if g.Visibility != "selected" {
			writeJSON(w, 409, map[string]any{"message": "The group is not limited to selected repositories"})
			return
		}
		items := []map[string]any{}
		for _, rid := range g.Repos {
			if rp := s.Repos[rid]; rp != nil {
				items = append(items, map[string]any{"id": rp.ID, "full_name": rp.FullName(), "name": rp.Name})
			}
		}
		writeJSON(w, 200, map[string]any{"total_count": len(items), "repositories": items})
	case sub == "repositories" && r.Method == "PUT":
		var in struct {
			Repos []int64 `json:"selected_repository_ids"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Repos == nil {
			writeJSON(w, 422, map[string]any{"message": "Validation Failed"})
			return
		}
		if g.Visibility != "selected" {
			writeJSON(w, 409, map[string]any{"message": "The group is not limited to selected repositories"})
			return
		}
		g.Repos = in.Repos
		w.WriteHeader(204)
	default:
		notFound(w)
	}
}

func (s *Server) runnerRoutes(w http.ResponseWriter, r *http.Request, scope, rest string) {
	rest = strings.TrimPrefix(rest, "/")
	switch {
	case rest == "registration-token" && r.Method == "POST":
		s.nextID++
		tok := fmt.Sprintf("FAKEREG%dTOKEN", s.nextID)
		s.regToks[tok] = true
		writeJSON(w, 201, map[string]any{"token": tok, "expires_at": s.Now().Add(time.Hour).UTC().Format(time.RFC3339)})
	case rest == "remove-token" && r.Method == "POST":
		s.nextID++
		tok := fmt.Sprintf("FAKEREM%dTOKEN", s.nextID)
		s.removeToks[tok] = true
		writeJSON(w, 201, map[string]any{"token": tok, "expires_at": s.Now().Add(time.Hour).UTC().Format(time.RFC3339)})
	case rest == "generate-jitconfig" && r.Method == "POST":
		var in struct {
			Name    string   `json:"name"`
			Group   int64    `json:"runner_group_id"`
			Labels  []string `json:"labels"`
			WorkDir string   `json:"work_folder"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Name == "" || in.Group == 0 {
			writeJSON(w, 422, map[string]any{"message": "Validation Failed"})
			return
		}
		s.nextID++
		cfg := fmt.Sprintf("FAKEJIT%dCONFIG", s.nextID)
		s.jitCfgs[cfg] = in.Name
		s.jitReqs = append(s.jitReqs, JITRequest{Scope: scope, Name: in.Name, Group: in.Group, Labels: in.Labels})
		writeJSON(w, 201, map[string]any{"runner": map[string]any{"id": 9000 + s.nextID, "name": in.Name}, "encoded_jit_config": cfg})
	case rest == "" && r.Method == "GET":
		items := []map[string]any{}
		for _, rn := range s.Runners[scope] {
			labels := []map[string]any{}
			for _, l := range rn.Labels {
				labels = append(labels, map[string]any{"name": l})
			}
			items = append(items, map[string]any{"id": rn.ID, "name": rn.Name, "os": rn.OS, "status": rn.Status, "busy": rn.Busy, "labels": labels})
		}
		writeJSON(w, 200, map[string]any{"total_count": len(items), "runners": items})
	case r.Method == "DELETE":
		id, _ := strconv.ParseInt(rest, 10, 64)
		list := s.Runners[scope]
		for i, rn := range list {
			if rn.ID == id {
				if rn.Busy {
					writeJSON(w, 422, map[string]string{"message": "Runner is busy"})
					return
				}
				s.Runners[scope] = append(list[:i:i], list[i+1:]...)
				w.WriteHeader(204)
				return
			}
		}
		notFound(w)
	default:
		notFound(w)
	}
}

func (s *Server) repoSecretKey(full string) *[2][32]byte {
	if s.secretKeys == nil {
		s.secretKeys = map[string]*[2][32]byte{}
	}
	k := s.secretKeys[full]
	if k == nil {
		pub, priv, err := box.GenerateKey(rand.Reader)
		if err != nil {
			panic(err) // no randomness: not a state a test can continue in
		}
		k = &[2][32]byte{*pub, *priv}
		s.secretKeys[full] = k
	}
	return k
}

func (s *Server) secretPublicKey(w http.ResponseWriter, full string) {
	k := s.repoSecretKey(full)
	writeJSON(w, 200, map[string]string{"key_id": "fake-key-" + full, "key": base64.StdEncoding.EncodeToString(k[0][:])})
}

func (s *Server) putSecret(w http.ResponseWriter, r *http.Request, full, name string) {
	var in struct {
		Encrypted string `json:"encrypted_value"`
		KeyID     string `json:"key_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.KeyID != "fake-key-"+full {
		writeJSON(w, 422, map[string]string{"message": "Bad key_id or body"})
		return
	}
	sealed, err := base64.StdEncoding.DecodeString(in.Encrypted)
	k := s.repoSecretKey(full)
	if err != nil {
		writeJSON(w, 422, map[string]string{"message": "Bad encrypted_value"})
		return
	}
	plain, ok := box.OpenAnonymous(nil, sealed, &k[0], &k[1])
	if !ok {
		writeJSON(w, 422, map[string]string{"message": "The value was not sealed to this repository's key"})
		return
	}
	if s.secrets == nil {
		s.secrets = map[string]map[string]string{}
	}
	if s.secrets[full] == nil {
		s.secrets[full] = map[string]string{}
	}
	_, existed := s.secrets[full][name]
	s.secrets[full][name] = string(plain)
	if existed {
		w.WriteHeader(204)
		return
	}
	w.WriteHeader(201)
}

func (s *Server) variableRoutes(w http.ResponseWriter, r *http.Request, full, name string) {
	name = strings.ToUpper(name) // GitHub keeps variable names in upper case
	vars := s.Variables[full]
	switch {
	case name == "" && r.Method == "GET":
		names := make([]string, 0, len(vars))
		for n := range vars {
			names = append(names, n)
		}
		sort.Strings(names)
		items := []map[string]string{}
		for _, n := range names {
			items = append(items, map[string]string{"name": n, "value": vars[n]})
		}
		writeJSON(w, 200, map[string]any{"total_count": len(items), "variables": items})
	case name == "" && r.Method == "POST":
		var in map[string]string
		_ = json.NewDecoder(r.Body).Decode(&in)
		in["name"] = strings.ToUpper(in["name"])
		if _, exists := vars[in["name"]]; exists {
			writeJSON(w, 409, map[string]string{"message": "Already exists"})
			return
		}
		if s.Variables[full] == nil {
			s.Variables[full] = map[string]string{}
		}
		s.Variables[full][in["name"]] = in["value"]
		w.WriteHeader(201)
	case r.Method == "PATCH":
		if _, exists := vars[name]; !exists {
			notFound(w)
			return
		}
		var in map[string]string
		_ = json.NewDecoder(r.Body).Decode(&in)
		vars[name] = in["value"]
		w.WriteHeader(204)
	case r.Method == "DELETE":
		if _, exists := vars[name]; !exists {
			notFound(w)
			return
		}
		delete(vars, name)
		w.WriteHeader(204)
	default:
		notFound(w)
	}
}

// gitTree answers the recursive tree of any ref with the files set by
// SetWorkflow (every path, not only workflows), each a blob with its size.
// TreeTruncated makes it answer a cut-short tree.
func (s *Server) gitTree(w http.ResponseWriter, full string) {
	files := s.WorkflowFiles[full]
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	items := []map[string]any{}
	for _, n := range names {
		sum := sha1.Sum([]byte(files[n]))
		items = append(items, map[string]any{"path": n, "mode": "100644", "type": "blob",
			"sha": hex.EncodeToString(sum[:]), "size": len(files[n])})
	}
	writeJSON(w, 200, map[string]any{"sha": "0123456789abcdef0123456789abcdef01234567", "tree": items, "truncated": s.TreeTruncated})
}

func (s *Server) contents(w http.ResponseWriter, r *http.Request, full, p string) {
	files := s.WorkflowFiles[full]
	if p == ".github/workflows" {
		if len(files) == 0 {
			notFound(w)
			return
		}
		names := make([]string, 0, len(files))
		for n := range files {
			names = append(names, n)
		}
		sort.Strings(names)
		items := []map[string]any{}
		for _, n := range names {
			items = append(items, map[string]any{"name": path.Base(n), "path": n, "type": "file"})
		}
		writeJSON(w, 200, items)
		return
	}
	content, ok := files[p]
	if !ok {
		notFound(w)
		return
	}
	sum := sha256.Sum256([]byte(content))
	etag := `"` + hex.EncodeToString(sum[:8]) + `"`
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(304)
		return
	}
	writeJSON(w, 200, map[string]any{"path": p, "type": "file", "encoding": "base64",
		"content": base64.StdEncoding.EncodeToString([]byte(content))})
}

func jobJSON(j *Job) map[string]any {
	return map[string]any{"id": j.ID, "run_id": j.RunID, "run_attempt": 1, "name": j.Name, "workflow_name": "ci",
		"head_sha": "0123456789abcdef0123456789abcdef01234567", "head_branch": "main", "status": j.Status,
		"conclusion": nil, "runner_name": j.RunnerName, "labels": j.Labels,
		"html_url":   fmt.Sprintf("https://github.com/%s/actions/runs/%d/job/%d", j.Repo, j.RunID, j.ID),
		"created_at": "2026-11-14T10:00:00Z", "started_at": nil, "completed_at": nil}
}

func (s *Server) runs(w http.ResponseWriter, r *http.Request, full string) {
	status := r.URL.Query().Get("status")
	seen := map[int64]bool{}
	items := []map[string]any{}
	for _, j := range s.Jobs {
		if j.Repo != full || j.Status != status || seen[j.RunID] {
			continue
		}
		seen[j.RunID] = true
		head := j.HeadRepo
		if head == "" {
			head = full
		}
		ev := j.Event
		if ev == "" {
			ev = "push"
		}
		items = append(items, map[string]any{"id": j.RunID, "event": ev, "status": status, "head_repository": map[string]any{"full_name": head}})
	}
	body := map[string]any{"total_count": len(items), "workflow_runs": items}
	// An ETag over the body, and 304 for a repeat, like GitHub (R5.37).
	raw, _ := json.Marshal(body)
	sum := sha256.Sum256(raw)
	etag := `"` + hex.EncodeToString(sum[:8]) + `"`
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(304)
		return
	}
	writeJSON(w, 200, body)
}

func (s *Server) runJobs(w http.ResponseWriter, _ *http.Request, full, rest string) {
	id, _ := strconv.ParseInt(strings.Split(strings.TrimPrefix(rest, "actions/runs/"), "/")[0], 10, 64)
	items := []map[string]any{}
	for _, j := range s.Jobs {
		if j.Repo == full && j.RunID == id {
			items = append(items, jobJSON(j))
		}
	}
	writeJSON(w, 200, map[string]any{"total_count": len(items), "jobs": items})
}

// HasRegistrationToken and HasRemoveToken tell whether a token was issued and
// not yet used by RunnerConfig-style calls (tests of the one-use rule).
func (s *Server) HasRemoveToken(tok string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.removeToks[tok]
}

// HasRegistrationToken tells whether a registration token was issued.
func (s *Server) HasRegistrationToken(tok string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.regToks[tok]
}

// ApproveDevice approves a device code for a user, or denies it with 0.
func (s *Server) ApproveDevice(code string, userID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.DeviceCodes[code] = userID
}

// DeviceCodeIssued returns the last device code the fake handed out.
func (s *Server) DeviceCodeIssued() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	last := ""
	for c := range s.DeviceCodes {
		if c > last {
			last = c
		}
	}
	return last
}

func (s *Server) deviceToken(w http.ResponseWriter, code string) {
	uid, known := s.DeviceCodes[code]
	switch {
	case !known:
		writeJSON(w, 200, map[string]string{"error": "bad_verification_code"})
	case uid == 0:
		s.devicePolls++
		if s.DeviceSlowDownOnce && s.devicePolls == 1 {
			writeJSON(w, 200, map[string]string{"error": "slow_down", "interval": "10"})
			return
		}
		writeJSON(w, 200, map[string]string{"error": "authorization_pending"})
	case uid < 0:
		writeJSON(w, 200, map[string]string{"error": "access_denied"})
	default:
		tok := "ghu_" + "FAKE" + strconv.FormatInt(uid, 10) + strings.Repeat("d", 20)
		s.UserTokens[tok] = uid
		delete(s.DeviceCodes, code)
		writeJSON(w, 200, map[string]any{"access_token": tok, "token_type": "bearer"})
	}
}

// checkToken answers POST /applications/{client_id}/token (R5.14).
func (s *Server) checkToken(w http.ResponseWriter, r *http.Request) {
	user, pass, ok := r.BasicAuth()
	if !ok || pass != s.ClientSecret || user == "" {
		unauthorized(w)
		return
	}
	var in map[string]string
	_ = json.NewDecoder(r.Body).Decode(&in)
	uid, found := s.UserTokens[in["access_token"]]
	if !found {
		notFound(w)
		return
	}
	u := s.Users[uid]
	writeJSON(w, 200, map[string]any{"user": map[string]any{"id": u.ID, "login": u.Login}})
}

// HasJITConfig tells whether a JIT config was issued.
func (s *Server) HasJITConfig(cfg string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.jitCfgs[cfg]
	return ok
}

// JITRequests returns the generate-jitconfig calls seen so far.
func (s *Server) JITRequests() []JITRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]JITRequest(nil), s.jitReqs...)
}
