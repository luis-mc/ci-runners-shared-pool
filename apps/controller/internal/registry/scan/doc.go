// Package scan is the image SBOM and vulnerability scan of the registry (REG8,
// docs/features/registry R5.59 to R5.62).
//
// Each promotion queues a scan work row. An idle Linux machine claims it and
// runs the pinned Syft (SBOM) and Trivy (vulnerabilities) against the
// registry reference by digest (Runner); nothing in the image runs. The
// controller keeps the counts by severity in registry_scans and stores the
// SBOM and the report as OCI referrers of the image (Scanner, Referrers). A
// failed scan only marks its row failed: it never blocks a pull or a
// promotion. The tool binaries come through pinned, sha256-checked downloads
// (Fetch, DL-94).
package scan
