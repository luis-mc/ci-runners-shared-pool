package install

import (
	"fmt"
	"github.com/luis-mc/ci-runners-shared-pool/internal/addresses"
	"strings"
)

// OSRelease is the part of /etc/os-release the Docker repository line needs.
type OSRelease struct {
	ID, IDLike, Codename, UbuntuCodename string
}

// ParseOSRelease reads the text of /etc/os-release.
func ParseOSRelease(text string) OSRelease {
	var o OSRelease
	for _, ln := range strings.Split(text, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(ln), "=")
		if !ok {
			continue
		}
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		switch k {
		case "ID":
			o.ID = strings.ToLower(v)
		case "ID_LIKE":
			o.IDLike = strings.ToLower(v)
		case "VERSION_CODENAME":
			o.Codename = v
		case "UBUNTU_CODENAME":
			o.UbuntuCodename = v
		}
	}
	return o
}

// DockerRepo returns the Docker apt repository family ("ubuntu" or "debian")
// and the codename, or ok=false for any other family.
func (o OSRelease) DockerRepo() (family, codename string, ok bool) {
	fam := ""
	switch {
	case o.ID == "ubuntu":
		fam = "ubuntu"
	case o.ID == "debian":
		fam = "debian"
	case hasWord(o.IDLike, "ubuntu"):
		fam = "ubuntu"
	case hasWord(o.IDLike, "debian"):
		fam = "debian"
	default:
		return "", "", false
	}
	cn := o.Codename
	if fam == "ubuntu" && o.UbuntuCodename != "" {
		cn = o.UbuntuCodename
	}
	if cn == "" || !safeWord(cn) {
		return "", "", false
	}
	return fam, cn, true
}

// DockerRPMRepo returns the Docker rpm repository family ("fedora", "rhel" or
// "centos") for a dnf distribution, or ok=false for any other family. Rocky
// Linux, AlmaLinux and other RHEL rebuilds use Docker's centos repository.
func (o OSRelease) DockerRPMRepo() (family string, ok bool) {
	switch {
	case o.ID == "fedora":
		return "fedora", true
	case o.ID == "rhel":
		return "rhel", true
	case o.ID == "centos", o.ID == "rocky", o.ID == "almalinux", o.ID == "ol":
		return "centos", true
	case hasWord(o.IDLike, "fedora"), hasWord(o.IDLike, "rhel"), hasWord(o.IDLike, "centos"):
		return "centos", true
	}
	return "", false
}

func hasWord(s, w string) bool {
	for _, f := range strings.Fields(s) {
		if f == w {
			return true
		}
	}
	return false
}

func safeWord(s string) bool {
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' && r != '.' {
			return false
		}
	}
	return s != ""
}

// Fixed Docker repository locations (what undo may remove).
const (
	KeyPath  = "/etc/apt/keyrings/docker.asc"
	RepoPath = "/etc/apt/sources.list.d/docker.list"
	// RPMRepoPath is the dnf source crsp adds on the rpm families.
	RPMRepoPath = "/etc/yum.repos.d/docker-ce.repo"
	// DistroName is the WSL distro crsp owns; no other distro is ever touched.
	DistroName = "crsp-docker"
)

// DockerPackages are the packages of Docker Engine from Docker's repository.
var DockerPackages = []string{"docker-ce", "docker-ce-cli", "containerd.io", "docker-buildx-plugin", "docker-compose-plugin"}

// RPMRepoURL is Docker's repository file for a rpm family.
func RPMRepoURL(family string) string {
	return addresses.DockerAptURL + "/" + family + "/docker-ce.repo"
}

// RPMConflictPackages are the unofficial packages Docker's install guide for
// the rpm families removes first.
var RPMConflictPackages = []string{"docker", "docker-client", "docker-client-latest", "docker-common", "docker-latest", "docker-latest-logrotate", "docker-logrotate", "docker-engine", "podman-docker"}

// BrewCask is the Homebrew cask of Docker Desktop on a Mac.
const BrewCask = "docker-desktop"

// ConflictPackages are the unofficial packages Docker's install guide removes first.
var ConflictPackages = []string{"docker.io", "docker-compose", "docker-compose-v2", "docker-doc", "podman-docker", "containerd", "runc"}

// RepoLine is the one line of docker.list.
func RepoLine(family, codename, arch string) string {
	return fmt.Sprintf("deb [arch=%s signed-by=%s] %s/%s %s stable\n", arch, KeyPath, addresses.DockerAptURL, family, codename)
}

// KeyURL is Docker's signing key for a family.
func KeyURL(family string) string { return addresses.DockerAptURL + "/" + family + "/gpg" }

// ManualDockerEngine is the "Do it myself" text for a family this release does not install.
func ManualDockerEngine() []string {
	return []string{
		"Follow Docker's install guide for your distribution: https://docs.docker.com/engine/install/ (crsp installs it for Ubuntu, Debian, Fedora and the Red Hat family; other families are done by hand)",
		"Then start the service: sudo systemctl enable --now docker",
	}
}
