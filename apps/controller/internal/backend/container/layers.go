package container

import "sort"

// ImageKeyLabel is the image label that holds the key of the layers and pinned
// versions the image was built from (runners R5.33, doctor R5.31).
const ImageKeyLabel = "org.crsp.image-key"

// Layer is one image layer and the tools the doctor looks for in the running
// image (runners R5.50). A Tools entry is a command name; an entry that starts
// with "$" names an environment variable that must hold an existing folder.
type Layer struct {
	Name  string
	Tools []string
	// Title, Purpose and Labels are the words the Images screen shows: the
	// name, the one line "pick it when", and the job labels that ask for it.
	Title   string
	Purpose string
	Labels  []string
}

// Layers are the fixed tool lists. The names are the dev-standards names, so a
// project setting maps one to one (runners R5.50). "base" is always present
// (runners R5.49).
var Layers = []Layer{
	{Name: "base", Tools: []string{"git", "curl", "python3", "jq", "zip", "unzip", "xz", "make", "gcc", "pkg-config", "psql", "gh", "node", "corepack", "pnpm"}},
	{Name: "windows-packaging", Title: "Windows packaging", Purpose: "your jobs build an MSI or run Windows tools under Wine", Labels: []string{"windows-packaging"}, Tools: []string{"dotnet", "wine", "wine64", "Xvfb", "xauth", "msiextract"}},
	{Name: "linux-packaging", Title: "Linux packaging", Purpose: "your jobs build deb or rpm packages", Labels: []string{"linux-packaging"}, Tools: []string{"dpkg-deb", "rpmbuild", "make", "checkmodule", "gpg", "shellcheck", "systemctl", "createrepo_c"}},
	{Name: "llvm", Title: "LLVM cross-compiler", Purpose: "your jobs build Windows programs on Linux with clang-cl", Labels: []string{"llvm"}, Tools: []string{"clang-cl", "lld-link", "llvm-lib"}},
	{Name: "playwright", Title: "Browsers for Playwright", Purpose: "your tests drive a browser", Labels: []string{"browsers", "playwright"}, Tools: []string{"$PLAYWRIGHT_BROWSERS_PATH"}},
	{Name: "kvm", Title: "KVM virtual machines", Purpose: "your jobs boot a virtual machine with qemu", Labels: []string{"kvm"}, Tools: []string{"qemu-system-x86_64", "qemu-img", "cloud-localds", "ssh"}},
}

// LayerTools returns the tools of a layer and whether the layer is known.
func LayerTools(name string) ([]string, bool) {
	for _, l := range Layers {
		if l.Name == name {
			return l.Tools, true
		}
	}
	return nil, false
}

// LayerNames lists the known layers, sorted.
func LayerNames() []string {
	n := make([]string, 0, len(Layers))
	for _, l := range Layers {
		n = append(n, l.Name)
	}
	sort.Strings(n)
	return n
}
