package scan

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store/sqlcgen"
)

// Media and artifact types of the two referrers.
const (
	SBOMArtifactType   = "application/spdx+json"
	ReportArtifactType = "application/vnd.aquasec.trivy.report+json"
)

// Queries is the part of sqlcgen.Queries the scanner uses.
type Queries interface {
	QueueRegistryScan(ctx context.Context, imageID string) error
	ListQueuedRegistryScans(ctx context.Context) ([]sqlcgen.ListQueuedRegistryScansRow, error)
	ClaimRegistryScan(ctx context.Context, arg sqlcgen.ClaimRegistryScanParams) (int64, error)
	FinishRegistryScan(ctx context.Context, arg sqlcgen.FinishRegistryScanParams) error
	GetRegistryScan(ctx context.Context, imageID string) (sqlcgen.RegistryScan, error)
	SetRegistryImageScanState(ctx context.Context, arg sqlcgen.SetRegistryImageScanStateParams) error
	GetRegistryImage(ctx context.Context, id string) (sqlcgen.RegistryImage, error)
}

// Referrers stores a blob as an OCI referrer of an image: an artifact manifest
// whose subject is the image's manifest digest. It returns the digest of the
// referrer manifest.
type Referrers interface {
	Put(ctx context.Context, name, subjectDigest, artifactType string, body io.Reader) (digest string, err error)
}

// Scanner implements registry.Scanner over the store. It never blocks a
// pull or a promotion (R5.61): every error here is the caller's to log.
type Scanner struct {
	q   Queries
	ref Referrers
	now func() time.Time
}

var _ registry.Scanner = (*Scanner)(nil)

// New returns a Scanner. now may be nil.
func New(q Queries, ref Referrers, now func() time.Time) *Scanner {
	if now == nil {
		now = time.Now
	}
	return &Scanner{q: q, ref: ref, now: now}
}

// Hook is the PromotedHook that queues a scan (R5.59).
func (s *Scanner) Hook() registry.PromotedHook {
	return func(ctx context.Context, img registry.Image) error { return s.Queue(ctx, img.ID) }
}

// Queue adds the scan work row of an image. Queuing twice keeps the first row.
func (s *Scanner) Queue(ctx context.Context, imageID string) error {
	if err := s.q.QueueRegistryScan(ctx, imageID); err != nil {
		return fmt.Errorf("scan: queue %s: %w", imageID, err)
	}
	return s.setImageState(ctx, imageID, registry.ScanQueued)
}

// Claim gives a machine the next queued scan; the machine that pushed an image
// gets its images first. ok is false when nothing waits.
func (s *Scanner) Claim(ctx context.Context, machineID string) (string, bool, error) {
	rows, err := s.q.ListQueuedRegistryScans(ctx)
	if err != nil {
		return "", false, fmt.Errorf("scan: list queued: %w", err)
	}
	ordered := make([]string, 0, len(rows))
	for _, r := range rows {
		if r.PushedBy == machineID {
			ordered = append(ordered, r.ImageID)
		}
	}
	for _, r := range rows {
		if r.PushedBy != machineID {
			ordered = append(ordered, r.ImageID)
		}
	}
	for _, id := range ordered {
		n, err := s.q.ClaimRegistryScan(ctx, sqlcgen.ClaimRegistryScanParams{
			MachineID: machineID, ClaimedAt: s.stamp(), ImageID: id,
		})
		if err != nil {
			return "", false, fmt.Errorf("scan: claim %s: %w", id, err)
		}
		if n == 1 { // another machine may have won the row
			if err := s.setImageState(ctx, id, registry.ScanRunning); err != nil {
				return "", false, err
			}
			return id, true, nil
		}
	}
	return "", false, nil
}

// Record keeps the result of a claimed scan. A result with Error, or a failure
// to store a referrer, marks the row failed with the reason; the counts of a
// failed scan stay zero. Readers are consumed once.
func (s *Scanner) Record(ctx context.Context, r registry.ScanResult) error {
	row, err := s.q.GetRegistryScan(ctx, r.ImageID)
	if err != nil {
		return fmt.Errorf("scan: row of %s: %w", r.ImageID, err)
	}
	if row.State != "scanning" {
		return fmt.Errorf("scan: %s is %s, not scanning", r.ImageID, row.State)
	}
	if r.MachineID != "" && row.MachineID != r.MachineID {
		return fmt.Errorf("scan: %s is claimed by another machine", r.ImageID)
	}
	fin := sqlcgen.FinishRegistryScanParams{ImageID: r.ImageID, FinishedAt: s.stamp()}
	state := registry.ScanDone
	var sbomDigest, reportDigest string
	if r.Error == "" {
		sbomDigest, reportDigest, err = s.storeReferrers(ctx, r)
		if err != nil {
			r.Error = err.Error()
		}
	}
	if r.Error != "" {
		state = registry.ScanFailed
		fin.Error = clip(r.Error)
	} else {
		fin.Packages, fin.Critical, fin.High, fin.Medium, fin.Low =
			int64(r.Packages), int64(r.Critical), int64(r.High), int64(r.Medium), int64(r.Low)
		fin.SbomDigest, fin.ReportDigest = sbomDigest, reportDigest
	}
	fin.State = string(state)
	if err := s.q.FinishRegistryScan(ctx, fin); err != nil {
		return fmt.Errorf("scan: finish %s: %w", r.ImageID, err)
	}
	return s.setImageState(ctx, r.ImageID, state)
}

// storeReferrers puts the SBOM and the report in the registry as referrers.
func (s *Scanner) storeReferrers(ctx context.Context, r registry.ScanResult) (sbom, report string, err error) {
	img, err := s.q.GetRegistryImage(ctx, r.ImageID)
	if err != nil {
		return "", "", fmt.Errorf("image %s: %w", r.ImageID, err)
	}
	if r.SBOM == nil || r.Report == nil {
		return "", "", errors.New("scan result has no SBOM or report")
	}
	if sbom, err = s.ref.Put(ctx, img.Name, img.ManifestDigest, SBOMArtifactType, r.SBOM); err != nil {
		return "", "", fmt.Errorf("store SBOM: %w", err)
	}
	if report, err = s.ref.Put(ctx, img.Name, img.ManifestDigest, ReportArtifactType, r.Report); err != nil {
		return "", "", fmt.Errorf("store report: %w", err)
	}
	return sbom, report, nil
}

func (s *Scanner) setImageState(ctx context.Context, id string, st registry.ScanState) error {
	err := s.q.SetRegistryImageScanState(ctx, sqlcgen.SetRegistryImageScanStateParams{ScanState: string(st), ID: id})
	if err != nil {
		return fmt.Errorf("scan: image %s state: %w", id, err)
	}
	return nil
}

func (s *Scanner) stamp() string { return s.now().UTC().Format("2006-01-02T15:04:05.000Z") }

func clip(msg string) string {
	msg = strings.TrimSpace(msg)
	if len(msg) > 500 {
		msg = msg[:500]
	}
	return msg
}
