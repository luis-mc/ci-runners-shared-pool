package github

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// MaxWebhookBody is the largest delivery accepted (R6.5).
const MaxWebhookBody = 25 << 20

// HMACVerifier checks X-Hub-Signature-256 in constant time (R6.5).
type HMACVerifier struct{ secret []byte }

// NewWebhookVerifier returns a verifier for the App's webhook secret.
func NewWebhookVerifier(secret []byte) *HMACVerifier {
	return &HMACVerifier{secret: append([]byte(nil), secret...)}
}

// Verify implements WebhookVerifier.
func (v *HMACVerifier) Verify(body []byte, signature256 string) bool {
	if len(v.secret) == 0 {
		return false
	}
	hexSig, ok := strings.CutPrefix(signature256, "sha256=")
	if !ok {
		return false
	}
	got, err := hex.DecodeString(hexSig)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, v.secret)
	mac.Write(body)
	return hmac.Equal(mac.Sum(nil), got)
}

// Sign makes the header value for a body, for tests and the fake server.
func Sign(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// WebhookEvents receives verified, de-duplicated deliveries.
type WebhookEvents interface {
	// Job gets a workflow_job event (R5.36).
	Job(Job)
	// Installation gets an installation or installation_repositories event
	// (R5.22): the raw body, for the reconcile to read.
	Installation(event string, body []byte)
}

// RepositoryEvents is implemented by an events receiver that wants the
// `repository` webhook (CRSP82): action is `publicized`, `privatized`,
// `renamed` and so on.
type RepositoryEvents interface {
	Repository(action, fullName string)
}

// WebhookHandler serves POST /webhooks/github (R5.36, R6.5).
type WebhookHandler struct {
	// Verifier is loaded per request so a rotated secret takes effect.
	Verifier func() (WebhookVerifier, error)
	// FirstDelivery records a delivery id and reports false for a repeat.
	FirstDelivery func(id, event string) (bool, error)
	Events        WebhookEvents
	// OnDelivery sees every verified delivery: the repository (owner/name, ""
	// when the body names none), the job id (0 when none) and whether it was
	// used (webhook health, RES14). Nil does nothing.
	OnDelivery func(repository string, jobID int64, used bool)
}

// ServeHTTP implements http.Handler. The signature is checked before the body
// is parsed.
func (h *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxWebhookBody))
	if err != nil {
		http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
		return
	}
	v, err := h.Verifier()
	if err != nil {
		http.Error(w, "webhook secret unavailable", http.StatusServiceUnavailable)
		return
	}
	if !v.Verify(body, r.Header.Get("X-Hub-Signature-256")) {
		http.Error(w, "bad signature", http.StatusUnauthorized)
		return
	}
	id, event := r.Header.Get("X-GitHub-Delivery"), r.Header.Get("X-GitHub-Event")
	if id == "" || event == "" {
		http.Error(w, "missing delivery headers", http.StatusBadRequest)
		return
	}
	first, err := h.FirstDelivery(id, event)
	if err != nil {
		http.Error(w, "could not record delivery", http.StatusInternalServerError)
		return
	}
	if !first {
		w.WriteHeader(http.StatusOK) // a repeat is acknowledged and ignored
		return
	}
	switch event {
	case "workflow_job":
		detail, err := ParseWorkflowJobDetail(body)
		job := detail.Job
		if err != nil {
			if h.OnDelivery != nil {
				h.OnDelivery(bodyRepository(body), 0, false)
			}
			http.Error(w, "unreadable workflow_job", http.StatusBadRequest)
			return
		}
		h.Events.Job(job)
		if de, ok := h.Events.(JobDetailEvents); ok {
			de.JobDetail(detail)
		}
		if h.OnDelivery != nil {
			h.OnDelivery(job.Repository, job.ID, true)
		}
	case "installation", "installation_repositories":
		h.Events.Installation(event, body)
	case "repository":
		var ev struct {
			Action     string `json:"action"`
			Repository struct {
				FullName string `json:"full_name"`
			} `json:"repository"`
		}
		if err := json.Unmarshal(body, &ev); err != nil || ev.Repository.FullName == "" {
			http.Error(w, "unreadable repository event", http.StatusBadRequest)
			return
		}
		if re, ok := h.Events.(RepositoryEvents); ok {
			re.Repository(ev.Action, ev.Repository.FullName)
		}
	}
	w.WriteHeader(http.StatusOK)
}

// InstallationEvent is the part of an installation webhook the grant reads.
type InstallationEvent struct {
	Action         string
	InstallationID int64
}

// NewPermissionsAccepted is the action GitHub sends when an installation's
// owner accepts the permissions the App asked for since (CRSP139, R5.87).
const NewPermissionsAccepted = "new_permissions_accepted"

// ParseInstallationEvent reads the action and installation id of an
// installation or installation_repositories body.
func ParseInstallationEvent(body []byte) (InstallationEvent, error) {
	var ev struct {
		Action       string `json:"action"`
		Installation struct {
			ID int64 `json:"id"`
		} `json:"installation"`
	}
	if err := json.Unmarshal(body, &ev); err != nil {
		return InstallationEvent{}, err
	}
	return InstallationEvent{Action: ev.Action, InstallationID: ev.Installation.ID}, nil
}

// bodyRepository reads repository.full_name from a delivery body, "" when absent.
func bodyRepository(body []byte) string {
	var ev struct {
		Repository struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
	}
	if json.Unmarshal(body, &ev) != nil {
		return ""
	}
	return ev.Repository.FullName
}

// ParseWorkflowJobEvent reads a workflow_job webhook body.
func ParseWorkflowJobEvent(body []byte) (Job, error) {
	ev, err := ParseWorkflowJobDetail(body)
	return ev.Job, err
}
