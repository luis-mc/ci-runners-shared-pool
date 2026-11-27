package agent

import (
	"context"
	"os"
	"strings"
)

// BaseImage is one runner base image a backend needs on this machine: a
// container image (by digest or tag) or a VM base disk (runners R5.74).
type BaseImage struct {
	Backend string // docker, podman, kvm, ...
	Ref     string // image digest or tag, or the base disk path
	Present bool
	// Fix says what to do when the image is missing.
	Fix string
}

// BaseImageSource lists the base images the machine needs and whether each
// exists. An error means the probe could not run: nothing is known, so the
// machine is not held for it.
type BaseImageSource func(ctx context.Context) ([]BaseImage, error)

// MissingBaseImages keeps the entries that are not present.
func MissingBaseImages(all []BaseImage) []BaseImage {
	var out []BaseImage
	for _, b := range all {
		if !b.Present {
			out = append(out, b)
		}
	}
	return out
}

// SetBaseImageGaps records the base images the machine lacks. While any is
// missing the machine is not accepting, with pause reason base_image_missing.
func (g *Gate) SetBaseImageGaps(missing []BaseImage) {
	g.mu.Lock()
	g.baseGaps = append([]BaseImage(nil), missing...)
	g.mu.Unlock()
}

// BaseImageGaps returns the base images the machine lacks.
func (g *Gate) BaseImageGaps() []BaseImage {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]BaseImage(nil), g.baseGaps...)
}

// checkBaseImages refreshes the gate from the configured source.
func (l *link) checkBaseImages(ctx context.Context) {
	if l.cfg.BaseImages == nil {
		return
	}
	all, err := l.cfg.BaseImages(ctx)
	if err != nil {
		l.cfg.Logger.Warn("the base images could not be checked", "err", err)
		return
	}
	missing := MissingBaseImages(all)
	for _, m := range missing {
		l.cfg.Logger.Warn("a runner base image is missing; the machine takes no jobs", "backend", m.Backend, "image", m.Ref, "fix", m.Fix)
	}
	l.cfg.Gate.SetBaseImageGaps(missing)
}

// ContainerBaseImage checks a container image by reference (digest or tag)
// through `docker image inspect`.
func ContainerBaseImage(sys Sys, backend, engine, ref string) BaseImage {
	b := BaseImage{Backend: backend, Ref: ref,
		Fix: "Build or pull the image: crsp images build, or `" + engine + " pull " + ref + "`."}
	res, ok := sys.Run([]string{engine, "image", "inspect", "--format", "{{.Id}}", ref})
	b.Present = ok && res.Code == 0 && strings.TrimSpace(res.Out) != ""
	return b
}

// VMBaseDisk checks that a VM base disk file exists.
func VMBaseDisk(backend, path string) BaseImage {
	st, err := os.Stat(path)
	return BaseImage{Backend: backend, Ref: path, Present: err == nil && st.Mode().IsRegular(),
		Fix: "Prepare the base disk again: crsp images build, then check the VM cache folder."}
}
