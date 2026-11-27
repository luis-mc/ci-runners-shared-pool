package scan

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry"
)

// Runner runs Syft and Trivy on a machine against a registry reference by
// digest. Both tools read the image from the registry; nothing in the image is
// started (R5.59).
type Runner struct {
	Syft    string        // path of the pinned syft binary
	Trivy   string        // path of the pinned trivy binary
	Env     []string      // extra environment, e.g. registry credentials for the tools
	Timeout time.Duration // per tool; zero means 15 minutes
}

// Ref is the reference to scan: host/name@digest, always by digest.
func Ref(host string, img registry.Image) string {
	return strings.TrimRight(host, "/") + "/" + img.Name + "@" + img.Manifest
}

// Scan scans ref and returns the result for Scanner.Record. A tool failure is
// returned in ScanResult.Error, not as an error: the scan failed, the caller
// still reports it. An error means the run could not start at all.
func (r Runner) Scan(ctx context.Context, imageID, machineID, ref string) (registry.ScanResult, error) {
	res := registry.ScanResult{ImageID: imageID, MachineID: machineID}
	if !strings.Contains(ref, "@sha256:") {
		return res, fmt.Errorf("scan: reference %q is not by digest", ref)
	}
	dir, err := os.MkdirTemp("", "crsp-scan-")
	if err != nil {
		return res, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	sbomFile, reportFile := filepath.Join(dir, "sbom.spdx.json"), filepath.Join(dir, "report.json")

	if out, err := r.run(ctx, r.Syft, "registry:"+ref, "--quiet", "-o", "spdx-json="+sbomFile); err != nil {
		res.Error = "syft: " + failure(err, out)
		return res, nil
	}
	if out, err := r.run(ctx, r.Trivy, "image", "--quiet", "--format", "json", "--output", reportFile, "--scanners", "vuln", ref); err != nil {
		res.Error = "trivy: " + failure(err, out)
		return res, nil
	}
	sbom, err := os.ReadFile(sbomFile)
	if err != nil {
		res.Error = "syft wrote no SBOM"
		return res, nil
	}
	report, err := os.ReadFile(reportFile)
	if err != nil {
		res.Error = "trivy wrote no report"
		return res, nil
	}
	if res.Packages, err = CountPackages(sbom); err != nil {
		res.Error = "SBOM: " + err.Error()
		return res, nil
	}
	if res.Critical, res.High, res.Medium, res.Low, err = CountSeverities(report); err != nil {
		res.Error = "report: " + err.Error()
		return res, nil
	}
	res.SBOM, res.Report = bytes.NewReader(sbom), bytes.NewReader(report)
	return res, nil
}

func (r Runner) run(ctx context.Context, bin string, args ...string) ([]byte, error) {
	if bin == "" {
		return nil, errors.New("tool path is empty")
	}
	to := r.Timeout
	if to == 0 {
		to = 15 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, to)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Env = append(os.Environ(), "SYFT_CHECK_FOR_APP_UPDATE=false", "TRIVY_DISABLE_TELEMETRY=true")
	cmd.Env = append(cmd.Env, r.Env...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	if ctx.Err() != nil {
		return out.Bytes(), fmt.Errorf("timed out after %s", to)
	}
	return out.Bytes(), err
}

func failure(err error, out []byte) string {
	msg := strings.TrimSpace(string(out))
	if i := strings.LastIndex(msg, "\n"); i >= 0 && len(msg) > 300 {
		msg = msg[i+1:]
	}
	if msg == "" {
		return err.Error()
	}
	return err.Error() + ": " + msg
}

// CountPackages counts the packages of an SPDX JSON document. A document that
// is not SPDX is an error.
func CountPackages(sbom []byte) (int, error) {
	var d struct {
		SPDXVersion string            `json:"spdxVersion"`
		Packages    []json.RawMessage `json:"packages"`
	}
	if err := json.Unmarshal(sbom, &d); err != nil {
		return 0, fmt.Errorf("not JSON: %w", err)
	}
	if !strings.HasPrefix(d.SPDXVersion, "SPDX-") {
		return 0, errors.New("not an SPDX document")
	}
	return len(d.Packages), nil
}

// CountSeverities counts the vulnerabilities of a Trivy JSON report by
// severity. Unknown severities are not counted.
func CountSeverities(report []byte) (critical, high, medium, low int, err error) {
	var d struct {
		SchemaVersion int `json:"SchemaVersion"`
		Results       []struct {
			Vulnerabilities []struct {
				Severity string `json:"Severity"`
			} `json:"Vulnerabilities"`
		} `json:"Results"`
	}
	if err := json.Unmarshal(report, &d); err != nil {
		return 0, 0, 0, 0, fmt.Errorf("not JSON: %w", err)
	}
	if d.SchemaVersion == 0 {
		return 0, 0, 0, 0, errors.New("not a Trivy report")
	}
	for _, res := range d.Results {
		for _, v := range res.Vulnerabilities {
			switch strings.ToUpper(v.Severity) {
			case "CRITICAL":
				critical++
			case "HIGH":
				high++
			case "MEDIUM":
				medium++
			case "LOW":
				low++
			}
		}
	}
	return critical, high, medium, low, nil
}
