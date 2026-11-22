// Package fake is a fake GitHub API server for tests (R6.9). No test calls
// github.com: tests point the client's APIBase and WebBase at Server.URL.
//
// It is stateful: tests set up the App, installations, users, repositories,
// variables, runners, workflow files and jobs, then assert on what the code
// under test changed.
package fake

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Request is one call the server saw.
type Request struct {
	Method, Path string
	Query        string
	Header       http.Header
}

// JITRequest is one generate-jitconfig call the fake saw.
type JITRequest struct {
	Scope  string
	Name   string
	Group  int64
	Labels []string
}

// Repo is a repository of the fake.
type Repo struct {
	ID            int64
	Owner, Name   string
	Private       bool
	DefaultBranch string
	Archived      bool
	Installation  int64
	// Admins lists user ids with the GitHub admin permission.
	Admins map[int64]bool
}

// FullName is owner/name.
func (r *Repo) FullName() string { return r.Owner + "/" + r.Name }

// User is a GitHub user of the fake.
type User struct {
	ID    int64
	Login string
}

// Install is an installation of the fake.
type Install struct {
	ID        int64
	Account   string
	Type      string
	Selection string
	Perms     map[string]string
	Suspended bool
	// Users who can see it.
	Users map[int64]bool
}

// Runner of the fake.
type Runner struct {
	ID     int64
	Name   string
	OS     string
	Status string
	Busy   bool
	Labels []string
}

// Job of the fake: a run with one job.
type Job struct {
	ID, RunID  int64
	Repo       string
	Name       string
	Status     string
	Event      string
	HeadRepo   string
	Labels     []string
	RunnerName string
}

// Group is an organization runner group of the fake.
type Group struct {
	ID         int64
	Name       string
	Visibility string // all, selected or private
	Default    bool
	// Repos are the repository ids of a selected group.
	Repos []int64
}

// Delivery is one webhook delivery of the App, as GET /app/hook/deliveries lists it.
type Delivery struct {
	ID          int64
	DeliveredAt time.Time
	Event       string
	StatusCode  int
}

// Server is the fake GitHub.
type Server struct {
	*httptest.Server

	mu sync.Mutex

	// AppID and AppPublicKey let the server check App JWTs. Leave the key nil
	// to skip the signature check.
	AppID        int64
	AppPublicKey *rsa.PublicKey

	Users         map[int64]*User
	UserTokens    map[string]int64 // user access token -> user id
	Installs      map[int64]*Install
	Repos         map[int64]*Repo
	Variables     map[string]map[string]string // full repo name -> name -> value
	Runners       map[string][]*Runner         // scope ("owner/repo" or "org") -> runners
	WorkflowFiles map[string]map[string]string // full repo name -> path -> content
	TreeTruncated bool                         // the git tree answer says it was cut short
	Jobs          []*Job
	// Deliveries are the App webhook deliveries, newest first.
	Deliveries    []Delivery
	ManifestCodes map[string]bool // unused codes
	// NoWebhookSecret makes a conversion return an empty webhook secret, as
	// GitHub does for an App made without a hook.
	NoWebhookSecret bool
	// Hook is the App webhook config set through PATCH /app/hook/config
	// (OPS25): url, secret and content_type as GitHub keeps them.
	Hook map[string]string
	// HookPatches counts the PATCH /app/hook/config calls.
	HookPatches int
	// AppEvents, when not nil, replaces the events GET /app reports.
	AppEvents      []string
	OAuthCodes     map[string]int64
	OAuthVerifiers map[string]string // code -> expected PKCE challenge
	// DeviceCodes: device code -> approving user id (0 pending, -1 denied).
	DeviceCodes map[string]int64
	// ForkApproval is the fork pull request approval policy per repository.
	ForkApproval map[string]string
	// OrgMembers: organization login (lower case) -> user login (lower case) ->
	// role ("admin" is an owner, "member" a member).
	OrgMembers map[string]map[string]string
	// Groups are the runner groups of an organization (lower case login).
	Groups             map[string][]*Group
	DeviceSlowDownOnce bool
	devicePolls        int
	// ClientSecret is what the token endpoint demands.
	ClientSecret string
	PEM          string

	instTokens map[string]int64
	tokensMade int
	tokenReqs  []string
	appPerms   map[string]string
	removeToks map[string]bool
	regToks    map[string]bool
	jitCfgs    map[string]string
	jitReqs    []JITRequest
	requests   []Request
	nextID     int64

	// Rate: limit, remaining, reset (unix seconds) sent on every answer.
	RateLimit, RateRemaining int
	RateReset                int64
	// Fail answers the next calls with these statuses (one per call), for
	// retry and limit tests. A status of 403 with FailRateLimit set is a
	// primary rate limit.
	failNext       []int
	failMatch      []failRule
	FailRateLimit  bool
	FailRetryAfter string
	// Now is the clock for token expiry.
	Now func() time.Time
}

// New starts a fake. Close it with Close (httptest).
func New() *Server {
	s := &Server{
		Users: map[int64]*User{}, UserTokens: map[string]int64{}, Installs: map[int64]*Install{},
		Repos: map[int64]*Repo{}, Variables: map[string]map[string]string{},
		Runners: map[string][]*Runner{}, WorkflowFiles: map[string]map[string]string{},
		ManifestCodes: map[string]bool{}, OAuthCodes: map[string]int64{}, OAuthVerifiers: map[string]string{}, DeviceCodes: map[string]int64{}, ForkApproval: map[string]string{}, OrgMembers: map[string]map[string]string{}, Groups: map[string][]*Group{},
		instTokens: map[string]int64{}, removeToks: map[string]bool{}, regToks: map[string]bool{}, jitCfgs: map[string]string{},
		RateLimit: 5000, RateRemaining: 5000,
		AppID: 1234567, nextID: 1000, Now: time.Now,
		ClientSecret: "FAKE-CLIENT-SECRET-NOT-REAL",
	}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	return s
}

// Requests returns the calls seen so far.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.requests...)
}

// Count returns how many calls matched method and path prefix.
func (s *Server) Count(method, pathPrefix string) int {
	n := 0
	for _, r := range s.Requests() {
		if r.Method == method && strings.HasPrefix(r.Path, pathPrefix) {
			n++
		}
	}
	return n
}

// FailNext makes the next calls answer with these statuses.
func (s *Server) FailNext(statuses ...int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failNext = append(s.failNext, statuses...)
}

type failRule struct {
	substr string
	status int
	left   int
}

// FailMatching makes the next n calls whose path contains substr answer with
// status, and lets every other call through.
func (s *Server) FailMatching(substr string, status, n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failMatch = append(s.failMatch, failRule{substr: substr, status: status, left: n})
}

// Lock runs fn with the state locked, for tests that edit state while the
// server runs.
func (s *Server) Lock(fn func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn()
}

// AddUser registers a user and returns a user access token for them.
func (s *Server) AddUser(id int64, login string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Users[id] = &User{ID: id, Login: login}
	tok := "ghu_" + "FAKE" + strconv.FormatInt(id, 10) + strings.Repeat("x", 20)
	s.UserTokens[tok] = id
	return tok
}

// AddInstall registers an installation visible to the given users.
func (s *Server) AddInstall(id int64, account, typ string, users ...int64) *Install {
	s.mu.Lock()
	defer s.mu.Unlock()
	in := &Install{ID: id, Account: account, Type: typ, Selection: "selected", Users: map[int64]bool{},
		Perms: map[string]string{"administration": "write", "actions": "read", "actions_variables": "write", "secrets": "write",
			"contents": "read", "metadata": "read", "organization_self_hosted_runners": "write"}}
	for _, u := range users {
		in.Users[u] = true
	}
	s.Installs[id] = in
	return in
}

// AddRepo registers a repository in an installation; admins hold the GitHub
// admin permission.
func (s *Server) AddRepo(id int64, install int64, owner, name string, private bool, admins ...int64) *Repo {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := &Repo{ID: id, Owner: owner, Name: name, Private: private, DefaultBranch: "main", Installation: install, Admins: map[int64]bool{}}
	for _, a := range admins {
		r.Admins[a] = true
	}
	s.Repos[id] = r
	return r
}

// IssueManifestCode makes a manifest code valid once.
func (s *Server) IssueManifestCode(code string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ManifestCodes[code] = true
}

// IssueOAuthCode makes an OAuth code valid once for a user, bound to a PKCE
// challenge.
func (s *Server) IssueOAuthCode(code string, userID int64, challenge string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.OAuthCodes[code] = userID
	s.OAuthVerifiers[code] = challenge
}

// SetWorkflow sets a workflow file of a repository.
func (s *Server) SetWorkflow(fullName, path, content string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.WorkflowFiles[fullName] == nil {
		s.WorkflowFiles[fullName] = map[string]string{}
	}
	s.WorkflowFiles[fullName][path] = content
}

// Variable reads a variable as GitHub holds it.
func (s *Server) Variable(fullName, name string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.Variables[fullName][strings.ToUpper(name)]
	return v, ok
}

// VariableNames lists the variable names of a repository, sorted, as GitHub
// holds them (upper case).
func (s *Server) VariableNames(fullName string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.Variables[fullName]))
	for n := range s.Variables[fullName] {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// SetAppPermissions sets what GET /app says the App asks for (CRSP139); nil
// goes back to the fixture.
func (s *Server) SetAppPermissions(m map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appPerms = m
}

// TokensMade counts the installation tokens issued so far.
func (s *Server) TokensMade() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tokensMade
}

// SetVariable changes a variable "outside the product" (drift tests).
func (s *Server) SetVariable(fullName, name, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Variables[fullName] == nil {
		s.Variables[fullName] = map[string]string{}
	}
	s.Variables[fullName][strings.ToUpper(name)] = value
}

// AddRunner registers a runner in a scope ("owner/repo" or an organization).
func (s *Server) AddRunner(scope string, r Runner) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.ID == 0 {
		s.nextID++
		r.ID = s.nextID
	}
	s.Runners[scope] = append(s.Runners[scope], &r)
}

// RunnerNames lists runner names of a scope.
func (s *Server) RunnerNames(scope string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, r := range s.Runners[scope] {
		out = append(out, r.Name)
	}
	return out
}

// AddJob adds a run with one job.
func (s *Server) AddJob(j Job) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Jobs = append(s.Jobs, &j)
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.requests = append(s.requests, Request{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Header: r.Header.Clone()})
	var forced int
	if len(s.failNext) > 0 {
		forced, s.failNext = s.failNext[0], s.failNext[1:]
	}
	if forced == 0 {
		for i := range s.failMatch {
			fr := &s.failMatch[i]
			if fr.left > 0 && strings.Contains(r.URL.Path, fr.substr) {
				fr.left--
				forced = fr.status
				break
			}
		}
	}
	limit, rem, reset := s.RateLimit, s.RateRemaining, s.RateReset
	if reset == 0 {
		reset = s.Now().Add(time.Hour).Unix()
	}
	failRL, retryAfter := s.FailRateLimit, s.FailRetryAfter
	s.mu.Unlock()

	w.Header().Set("X-RateLimit-Limit", strconv.Itoa(limit))
	w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(rem))
	w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset, 10))
	if forced != 0 {
		if forced == 403 || forced == 429 {
			if failRL {
				w.Header().Set("X-RateLimit-Remaining", "0")
			}
			if retryAfter != "" {
				w.Header().Set("Retry-After", retryAfter)
			}
		}
		writeJSON(w, forced, map[string]string{"message": "forced failure"})
		return
	}
	if r.Header.Get("X-GitHub-Api-Version") == "" && !strings.HasPrefix(r.URL.Path, "/login/") {
		writeJSON(w, 400, map[string]string{"message": "missing api version"})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.route(w, r)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func notFound(w http.ResponseWriter) { writeJSON(w, 404, map[string]string{"message": "Not Found"}) }
func unauthorized(w http.ResponseWriter) {
	writeJSON(w, 401, map[string]string{"message": "Bad credentials"})
}

func bearer(r *http.Request) string {
	return strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
}

// checkJWT validates an App JWT: RS256, issuer, lifetime within 10 minutes and
// (when a public key is set) the signature.
func (s *Server) checkJWT(tok string) bool {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	var c struct {
		Iss string `json:"iss"`
		Iat int64  `json:"iat"`
		Exp int64  `json:"exp"`
	}
	if json.Unmarshal(payload, &c) != nil || c.Iss != strconv.FormatInt(s.AppID, 10) {
		return false
	}
	if c.Exp-c.Iat > 600 || c.Exp < s.Now().Unix() {
		return false
	}
	if s.AppPublicKey != nil {
		sig, err := base64.RawURLEncoding.DecodeString(parts[2])
		if err != nil {
			return false
		}
		sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
		if rsa.VerifyPKCS1v15(s.AppPublicKey, crypto.SHA256, sum[:], sig) != nil {
			return false
		}
	}
	return true
}

func (s *Server) userFor(r *http.Request) (*User, bool) {
	id, ok := s.UserTokens[bearer(r)]
	if !ok {
		return nil, false
	}
	return s.Users[id], true
}

func (s *Server) installFor(r *http.Request) (int64, bool) {
	id, ok := s.instTokens[bearer(r)]
	return id, ok
}

// repoByName finds a repository by owner and name.
func (s *Server) repoByName(owner, name string) *Repo {
	for _, r := range s.Repos {
		if strings.EqualFold(r.Owner, owner) && strings.EqualFold(r.Name, name) {
			return r
		}
	}
	return nil
}

func page(r *http.Request, total int) (from, to int, link string) {
	per, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
	if per <= 0 || per > 100 {
		per = 30
	}
	pg, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if pg <= 0 {
		pg = 1
	}
	from = (pg - 1) * per
	if from > total {
		from = total
	}
	to = min(from+per, total)
	if to < total {
		q := r.URL.Query()
		q.Set("page", strconv.Itoa(pg+1))
		link = fmt.Sprintf(`<%s%s?%s>; rel="next"`, "http://"+r.Host, r.URL.Path, q.Encode())
	}
	return
}

// ForkApprovalOf returns the fork approval policy the controller set.
func (s *Server) ForkApprovalOf(fullName string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ForkApproval[fullName]
}

// AddOrgMember makes a user a member of an organization with a role: "admin"
// (an owner) or "member". The organization also gets its default runner group.
func (s *Server) AddOrgMember(org, login, role string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o := strings.ToLower(org)
	if s.OrgMembers[o] == nil {
		s.OrgMembers[o] = map[string]string{}
	}
	s.OrgMembers[o][strings.ToLower(login)] = role
	if len(s.Groups[o]) == 0 {
		s.Groups[o] = []*Group{{ID: 1, Name: "Default", Visibility: "all", Default: true}}
	}
}

// AddGroup adds a runner group to an organization and returns it.
func (s *Server) AddGroup(org, name, visibility string, repos ...int64) *Group {
	s.mu.Lock()
	defer s.mu.Unlock()
	o := strings.ToLower(org)
	s.nextID++
	g := &Group{ID: s.nextID, Name: name, Visibility: visibility, Repos: repos}
	s.Groups[o] = append(s.Groups[o], g)
	return g
}

// GroupByName returns a copy of the named runner group of an organization.
func (s *Server) GroupByName(org, name string) (Group, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, g := range s.Groups[strings.ToLower(org)] {
		if strings.EqualFold(g.Name, name) {
			c := *g
			c.Repos = append([]int64(nil), g.Repos...)
			return c, true
		}
	}
	return Group{}, false
}

// TokenRequestBodies returns the body of each installation token request, so a
// test can check that no permissions were asked for (CRSP148).
func (s *Server) TokenRequestBodies() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.tokenReqs...)
}
