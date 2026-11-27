package registry

import (
	"context"
	"io"
	"net/http"
	"time"
)

// This file is the contract between the tasks of the REG wave. Each interface
// names the task that implements it. A task that calls another's interface
// tests against a fake of it until the real one is joined. Changing a method
// here after the wave starts is a contract change: it goes through the
// orchestrator, never through a task branch alone.

// ---------------------------------------------------------------- store (REG4 owns the adapter)

// ImageStore reads and writes images, tags and layers. Implemented by REG4 in
// internal/registry/store_images.go over internal/store/sqlcgen
// (queries/registry_records.sql).
type ImageStore interface {
	Image(ctx context.Context, id string) (Image, error)
	// ImageByKey finds the promoted image of key k under any name, or
	// ErrNotFound. Staged, refused and deleted images are not found.
	ImageByKey(ctx context.Context, k Key) (Image, error)
	Images(ctx context.Context, f ImageFilter) ([]Image, error)
	PutImage(ctx context.Context, img Image) error
	SetState(ctx context.Context, id string, s State, keepUntil time.Time) error
	Layers(ctx context.Context, imageID string) ([]Layer, error)
	PutLayers(ctx context.Context, imageID string, layers []Layer) error
}

// ImageFilter narrows Images. Zero fields match everything.
type ImageFilter struct {
	Kind       Kind
	States     []State
	Repository string // owner/name, through pointers and source
	Arch       string
	Profile    string
}

// PointerStore keeps the pointers per repository, profile and arch.
// Implemented by REG3 (queries/registry_keys.sql).
type PointerStore interface {
	Pointer(ctx context.Context, repository, profile, arch string) (Pointer, error)
	PointersTo(ctx context.Context, imageID string) ([]Pointer, error)
	// MovePointer sets the pointer and keeps the previous image for rollback.
	MovePointer(ctx context.Context, p Pointer) error
}

// RecommendationStore keeps the open recommendations. Implemented by REG6
// (queries/registry_retention.sql). There is no Dismiss: owner decision.
type RecommendationStore interface {
	Recommendations(ctx context.Context) ([]Recommendation, error)
	// Upsert opens or refreshes the one recommendation of (rule, image).
	Upsert(ctx context.Context, r Recommendation) error
	// Close ends it because its condition ended or it was acted on.
	Close(ctx context.Context, id, why string) error
}

// SettingsStore keeps the registry settings (folder, budgets, days).
// Implemented by REG6 (queries/registry_retention.sql).
type SettingsStore interface {
	Settings(ctx context.Context) (Settings, error)
	PutSettings(ctx context.Context, s Settings, by string) error
}

// MirrorSettingsStore keeps the mirror settings. Implemented by REG9.
type MirrorSettingsStore interface {
	MirrorSettings(ctx context.Context) (MirrorSettings, error)
	PutMirrorSettings(ctx context.Context, s MirrorSettings, by string) error
}

// Store is every store part. The controller wires one value that has all.
type Store interface {
	ImageStore
	PointerStore
	RecommendationStore
	SettingsStore
	MirrorSettingsStore
}

// ---------------------------------------------------------------- core (REG1)

// Staged is a push to crsp/incoming that the agent reported (PushReport).
type Staged struct {
	MachineID string
	Name      string // the incoming name, crsp/incoming/<machine>/...
	Target    string // crsp/std/<profile> or crsp/custom/<owner>-<repo>/<profile>
	Key       Key
	Manifest  string // the digest the agent says it pushed
	KeyInput  KeyInput
	Labels    map[string]string
	Source    string // owner/name of the run
	Commit    string
	Branch    string
}

// Promoter checks a staged image and promotes it by a manifest copy, never by
// moving data (REG1, R5.5). It refuses when the digest differs from the
// manifest in staging, a required label is missing or wrong, the key does not
// equal ComputeKey(KeyInput), or the tag exists under Target (ErrTagExists).
// On success it records the image (ImageStore.PutImage), then calls the
// Promoted hooks (REG3 update detection, REG4 layer accounting, REG8 scan).
type Promoter interface {
	Promote(ctx context.Context, s Staged) (Image, error)
}

// PromotedHook is told about each promoted image. REG3, REG4 and REG8 each
// register one; a hook error is logged and never undoes the promotion.
type PromotedHook func(ctx context.Context, img Image) error

// TokenIssuer issues and checks registry tokens (REG1, R5.10, R5.11). A machine may
// pull crsp/std/* and crsp/custom/* and push only to
// crsp/incoming/<its own id>/*. People never get a token.
type TokenIssuer interface {
	Issue(ctx context.Context, machineID string, want []Scope) (Token, error)
	// Verify returns the machine id and granted scopes of a token, or an error
	// for an expired, forged or revoked one.
	Verify(ctx context.Context, token string) (machineID string, scopes []Scope, err error)
}

// Registry is the mounted Distribution v3 instance (REG1).
type Registry interface {
	// Handler serves /v2/ on the controller's HTTPS listener.
	Handler() http.Handler
	// SetReadOnly stops pushes and promotions; pulls keep working (R5.48).
	SetReadOnly(on bool)
	// DeleteManifest removes a manifest by digest; blobs wait for GC.
	DeleteManifest(ctx context.Context, name, digest string) error
	// GC runs mark and sweep; the caller holds the read-only window.
	GC(ctx context.Context, dryRun bool) (freedBytes int64, err error)
	// Events streams pull and push events with the machine of the token, for
	// REG4's pull metrics.
	Events() <-chan Event
}

// Event is one registry event (manifest GET is a pull).
type Event struct {
	At        time.Time
	Action    string // pull or push
	Name      string
	Digest    string
	MachineID string
}

// ---------------------------------------------------------------- keys (REG3) and inputs (IMG2, IMG3)

// StackSource gives the stack files of a repository at a commit (IMG2, the
// stack scan read at the run's commit, RES19 IMG-3). Only the files the
// stack rules list are returned, never others.
type StackSource interface {
	StackFiles(ctx context.Context, repository, commit string) ([]StackFile, error)
}

// ProfileSource gives the image profile a job needs and the parts of its key
// other than the stack hash (IMG3, RES19 IMG-5).
type ProfileSource interface {
	// ProfileFor picks the profile of a job by its repository and labels.
	ProfileFor(ctx context.Context, repository string, labels []string) (ProfileRef, error)
}

// ProfileRef is what the key needs from a profile.
type ProfileRef struct {
	ID         string
	Kind       Kind // standard or custom
	BaseName   string
	BaseDigest string
	Pins       map[string]string
	Toolchains []Toolchain
}

// Keyer computes the key a run needs (REG3): ProfileSource, StackSource and
// ComputeKey together.
type Keyer interface {
	KeyFor(ctx context.Context, repository, commit, arch string, labels []string) (Key, KeyInput, ProfileRef, error)
}

// UpdateDetector decides whether a promoted image is an update of the image a
// repository points to (REG3, RES20 5.3). It opens a replace Recommendation;
// it never moves a pointer.
type UpdateDetector interface {
	OnPromoted(ctx context.Context, img Image) error
}

// ---------------------------------------------------------------- agent questions (REG2 on the agent, REG4 on the controller)

// EnsureAnswer is the controller's answer to "have key K for arch A?".
type EnsureAnswer struct {
	Found    bool
	Name     string // full name to pull, crsp/std/... or crsp/custom/...
	Manifest string // the digest to pull by
	Size     int64
	// Busy is the controller answering but not able to look now: the agent
	// asks again; it does not build (owner decision: a job waits for the
	// controller; it builds only on a clear "not in registry").
	Busy bool
}

// Locator answers EnsureImage on the controller (REG4).
type Locator interface {
	Ensure(ctx context.Context, machineID string, k Key, arch string) (EnsureAnswer, error)
}

// PullRecord is a finished or progressing pull, from the agent (REG2 sends,
// REG4 keeps).
type PullRecord struct {
	// PullID is the agent's id of the pull (PullReport.pull_id): a resent report is one row.
	PullID string
	// Arch is the OCI arch of the image (PullReport.arch).
	Arch         string
	MachineID    string
	Key          Key
	Manifest     string
	Source       Source
	State        string // pulling, waiting_controller, done or failed
	BytesDone    int64
	BytesTotal   int64
	Seconds      float64
	SavedSeconds float64
	Repository   string
	JobID        string
	Error        string
	At           time.Time
}

// PushRecord is a push to staging, from the agent.
type PushRecord struct {
	Staged
	// PushID is the agent's id of the push (PushReport.push_id): a resent report is one row.
	PushID  string
	State   string // pushing, waiting_controller, pushed or failed
	Seconds float64
	Error   string
	At      time.Time
}

// Recorder keeps pull and push records and per-day pull totals (REG4).
type Recorder interface {
	RecordPull(ctx context.Context, r PullRecord) error
	RecordPush(ctx context.Context, r PushRecord) error
}

// ---------------------------------------------------------------- retention (REG6)

// Retention computes recommendations, deletes safely and runs the weekly
// cleanup with its read-only window (RES20 6).
type Retention interface {
	// Evaluate refreshes every recommendation as of now and acts on the due
	// ones of AloneRules. It never deletes an image with a pointer, one a
	// runner uses, or one inside keep_until.
	Evaluate(ctx context.Context, now time.Time) error
	// CheckDelete says what uses an image; an empty InUse may be deleted.
	CheckDelete(ctx context.Context, imageID string) (InUse, error)
	// Delete removes the manifest. Without confirm and with a non-empty InUse
	// it returns ErrInUse and the InUse.
	Delete(ctx context.Context, imageID string, confirm bool, by string) (InUse, error)
	// Cleanup runs the read-only window, GC and the record of bytes freed.
	Cleanup(ctx context.Context, trigger string) (freedBytes int64, err error)
	Budgets(ctx context.Context) ([]Budget, error)
}

// DiskInfo gives the free bytes of the volume of a folder; REG6 uses it for
// the half-the-free-disk rule. Each OS has its own implementation (REG1 adds
// it beside the existing disk guard).
type DiskInfo interface {
	Free(path string) (int64, error)
}

// ---------------------------------------------------------------- replace (REG7)

// Replacer runs replace plans and rollbacks (RES20 5.4). It moves pointers
// through PointerStore and marks the old image superseded with KeepUntil.
type Replacer interface {
	CreatePlan(ctx context.Context, newImageID string, keepDays int, by string) (ReplacePlan, error)
	Plan(ctx context.Context, id string) (ReplacePlan, error)
	Start(ctx context.Context, id string) (ReplacePlan, error)
	Stop(ctx context.Context, id string) (ReplacePlan, error)
	// Rollback moves the pointers back while KeepUntil has not passed.
	Rollback(ctx context.Context, id string, by string) (ReplacePlan, error)
}

// ---------------------------------------------------------------- scan (REG8)

// ScanResult is the outcome of Syft and Trivy on one image, by digest.
type ScanResult struct {
	ImageID   string
	MachineID string
	Packages  int
	Critical  int
	High      int
	Medium    int
	Low       int
	SBOM      io.Reader // stored as an OCI referrer of the image
	Report    io.Reader // stored as an OCI referrer of the image
	Error     string
}

// Scanner queues scan work rows and keeps their results (REG8). It never
// blocks a pull or a promotion.
type Scanner interface {
	Queue(ctx context.Context, imageID string) error
	// Claim gives an idle Linux machine the next scan, the pusher first.
	Claim(ctx context.Context, machineID string) (imageID string, ok bool, err error)
	Record(ctx context.Context, r ScanResult) error
}

// ---------------------------------------------------------------- mirror (REG9)

// Mirror is the pull-through mirror of upstream bases: a second Distribution
// instance in proxy mode on the same listener, under its own path (REG9).
type Mirror interface {
	Handler() http.Handler
	Apply(ctx context.Context, s MirrorSettings) error
}
