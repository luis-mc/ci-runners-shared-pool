package github

import (
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// The ids of the steps a missing App permission goes through (CRSP139, R5.86).
const (
	GrantStepAsk    = "ask"    // the App's owner asks for it on the App page
	GrantStepAccept = "accept" // the installation's owner accepts it on the installation page
)

// GrantStep is one step of a grant, with its live state.
type GrantStep struct {
	ID string
	// Done is true when every permission of the step is past it.
	Done bool
	// Permissions are the permissions still waiting at this step; empty
	// once the step is done.
	Permissions []string
	// URL is the GitHub page of the step.
	URL string
}

// Grant is the state of the permissions a task needs, read from both sides
// of GitHub: what the App asks for and what the installation accepted.
type Grant struct {
	Ready bool
	// Steps is always [ask, accept], in that order.
	Steps []GrantStep
	// Next is the id of the first step that is not done, "" when Ready.
	Next string
	// Missing is every needed permission that is not accepted yet, sorted.
	Missing []string
}

// NeedLevel is the level a permission is needed at: the level of
// RequiredPermissions, or write for a name it does not list.
func NeedLevel(name string) string {
	if l, ok := RequiredPermissions[name]; ok {
		return l
	}
	return "write"
}

func holds(m map[string]string, name string) bool {
	return accessRank[m[name]] >= accessRank[NeedLevel(name)]
}

// DiffGrant compares the needed permissions with what the App asks for
// (GET /app) and what the installation accepted (GET /app/installations/{id}).
// A permission the installation holds is done at both steps; one the App asks
// for and the installation does not hold waits at "accept"; the rest wait at
// "ask". The URLs are put on the steps by the caller.
func DiffGrant(need []string, asked, accepted map[string]string) Grant {
	var notAsked, notAccepted []string
	seen := map[string]bool{}
	for _, n := range need {
		if seen[n] {
			continue
		}
		seen[n] = true
		switch {
		case holds(accepted, n):
		case holds(asked, n):
			notAccepted = append(notAccepted, n)
		default:
			notAsked = append(notAsked, n)
		}
	}
	sort.Strings(notAsked)
	sort.Strings(notAccepted)
	waiting := append(append([]string{}, notAsked...), notAccepted...)
	sort.Strings(waiting)
	g := Grant{
		Ready:   len(waiting) == 0,
		Missing: waiting,
		Steps: []GrantStep{
			{ID: GrantStepAsk, Done: len(notAsked) == 0, Permissions: notAsked},
			{ID: GrantStepAccept, Done: len(waiting) == 0, Permissions: waiting},
		},
	}
	switch {
	case len(notAsked) > 0:
		g.Next = GrantStepAsk
	case len(notAccepted) > 0:
		g.Next = GrantStepAccept
	}
	return g
}

// GrantPages names the two GitHub pages that grant an App permission: the
// App's permissions page (the App's owner changes the request), then the
// installation page (the installation's owner accepts it).
func GrantPages(webBase string, app App, ins Installation) (appURL, installURL string) {
	slug := url.PathEscape(app.Slug)
	if strings.EqualFold(app.OwnerType, "Organization") && app.OwnerLogin != "" {
		appURL = webBase + "/organizations/" + url.PathEscape(app.OwnerLogin) + "/settings/apps/" + slug + "/permissions"
	} else {
		appURL = webBase + "/settings/apps/" + slug + "/permissions"
	}
	if strings.EqualFold(ins.AccountType, "Organization") && ins.AccountLogin != "" {
		installURL = webBase + "/organizations/" + url.PathEscape(ins.AccountLogin) + "/settings/installations/" + strconv.FormatInt(ins.ID, 10)
	} else {
		installURL = webBase + "/settings/installations/" + strconv.FormatInt(ins.ID, 10)
	}
	return appURL, installURL
}
