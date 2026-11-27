package container

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend"
	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/registry/builds"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/runners"
)

// ImageRepo is the repository name of every pool image: `crsp-pool:<key>`.
const ImageRepo = "crsp-pool"

const (
	labelLayers = "crsp.layers"
	labelOS     = "crsp.os"
)

// ImageTag is the tag of an image key.
func ImageTag(key string) string { return ImageRepo + ":" + key }

// KeyFor is the image key of a spec on this machine: the base, the layers and
// the pinned versions (runners R5.33).
func (b *Backend) KeyFor(spec backend.ImageSpec) string {
	return runners.ImageKey(runners.ImageKeyInput{Base: b.cfg.BaseName, Layers: spec.Layers, Pins: b.cfg.Pins, Extras: spec.Extras})
}

// PrepareImage implements backend.Backend. A machine builds each key once and
// reuses it (runners R5.33): when the image of the key is there, nothing is
// built. The build runs aside; no running pool container is touched.
func (b *Backend) PrepareImage(ctx context.Context, spec backend.ImageSpec) (backend.ImageRef, error) {
	if spec.OS != "" && spec.OS != "linux" {
		return backend.ImageRef{}, fmt.Errorf("the container backend builds Linux images only, not %s", spec.OS)
	}
	req := b.registryRequest(spec)
	key := req.Key
	tag := ImageTag(key)
	ctl := b.controller()
	if mb, ok, err := b.imageSize(ctx, tag); err != nil {
		return backend.ImageRef{}, err
	} else if ok {
		b.log("prepare image", "image "+key+" is there; not built again")
		if ctl != nil {
			b.pullReport(ctl, nextID("pull"), req, "", agentv1.PullReport_SOURCE_LOCAL, agentv1.PullReport_STATE_DONE, 0, 0, 0, "")
		}
		return backend.ImageRef{ID: tag, SizeMB: mb}, nil
	}
	// Not here: ask the controller (registry R5.13). A controller that does
	// not answer is waited for; the build is only for a clear "not found".
	var stage *agentv1.EnsureImageAnswer
	if ctl != nil {
		ans, err := b.ask(ctx, ctl, req)
		if err != nil {
			return backend.ImageRef{}, err
		}
		if ans.GetOutcome() == agentv1.EnsureImageAnswer_OUTCOME_FOUND {
			perr := b.pull(ctx, ctl, req, ans, tag)
			if perr == nil {
				if mb, ok, err := b.imageSize(ctx, tag); err == nil && ok {
					return backend.ImageRef{ID: tag, SizeMB: mb}, nil
				}
				perr = errors.New("the pulled image cannot be read")
			}
			if ctx.Err() != nil {
				return backend.ImageRef{}, ctx.Err()
			}
			b.log("prepare image", pullFailOpen+b.redact(perr.Error())+pullFailClose)
		} else {
			if ans.GetOutcome() == agentv1.EnsureImageAnswer_OUTCOME_NOT_FOUND {
				// The controller said the registry has no such image (R5.71).
				m := builds.MetaFrom(ctx)
				m.Reason = "not_in_registry"
				ctx = builds.WithMeta(ctx, m)
			}
			if ans.GetStagingName() != "" {
				stage = ans
			}
		}
	}
	// One build per machine at a time (decision D20): a second asker waits, then
	// finds the image built by the first.
	release, err := b.buildGate.Acquire(ctx)
	if err != nil {
		return backend.ImageRef{}, err
	}
	defer release()
	if mb, ok, err := b.imageSize(ctx, tag); err == nil && ok {
		b.log("prepare image", "image "+key+" was built while this one waited")
		return backend.ImageRef{ID: tag, SizeMB: mb}, nil
	}
	buildStart := b.now()
	dockerfile, buildContext := b.cfg.Dockerfile, b.cfg.BuildContext
	if dockerfile == "" || buildContext == "" {
		if b.cfg.Assets == nil {
			return backend.ImageRef{}, fmt.Errorf("no Dockerfile is set to build image %s", key)
		}
		dir, err := b.stageBuild(ctx, key, normArch(spec.Arch))
		if err != nil {
			return backend.ImageRef{}, err
		}
		defer func() { _ = os.RemoveAll(dir) }()
		buildContext = filepath.Join(dir, "linux-base")
		dockerfile = filepath.Join(buildContext, "Dockerfile")
	}
	layers := strings.Join(runners.NormalizeLayers(spec.Layers), ",")
	b.log("prepare image", "building image "+key+" with layers ["+layers+"]")
	args := []string{"build", "-t", tag,
		"--label", LabelImage + "=" + key, "--label", labelLayers + "=" + layers, "--label", labelOS + "=linux",
		"--build-arg", "CRSP_LAYERS=" + layers, "--build-arg", "EXTRA_PACKAGES=" + strings.Join(runners.NormalizeLayers(spec.Extras), " "), "-f", dockerfile, buildContext}
	mb, err := b.streamBuild(ctx, spec, key, args)
	if err != nil {
		return backend.ImageRef{}, err
	}
	if ctl != nil {
		b.pullReport(ctl, nextID("pull"), req, "", agentv1.PullReport_SOURCE_BUILT, agentv1.PullReport_STATE_DONE, 0, 0, b.now().Sub(buildStart).Seconds(), "")
		if stage != nil {
			b.pushAsync(ctx, ctl, req, stage, tag, b.now().Sub(buildStart))
		}
	}
	return backend.ImageRef{ID: tag, SizeMB: mb}, nil
}

func (b *Backend) imageSize(ctx context.Context, tag string) (mb int64, ok bool, err error) {
	res, err := b.run(ctx, "image", "inspect", "--format", "{{.Size}}", tag)
	if err != nil {
		return 0, false, err
	}
	if res.ExitCode != 0 {
		if strings.Contains(strings.ToLower(res.Stderr), "no such image") || strings.Contains(strings.ToLower(res.Stderr), "no such object") {
			return 0, false, nil
		}
		return 0, false, fmt.Errorf("image inspect: %s", b.redact(strings.TrimSpace(res.Stderr)))
	}
	n, perr := strconv.ParseInt(strings.TrimSpace(res.Stdout), 10, 64)
	if perr != nil {
		return 0, false, fmt.Errorf("image inspect: bad size %q", strings.TrimSpace(res.Stdout))
	}
	return n / (1 << 20), true, nil
}

// ListImages lists the images this backend built, with their layers and size
// (runners R5.32).
func (b *Backend) ListImages(ctx context.Context) ([]runners.Image, error) {
	res, err := b.run(ctx, "image", "ls", "--filter", "label="+LabelImage, "--format", "{{.Repository}}:{{.Tag}}")
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("image ls: %s", b.redact(strings.TrimSpace(res.Stderr)))
	}
	var out []runners.Image
	for _, tag := range strings.Fields(res.Stdout) {
		if !strings.HasPrefix(tag, ImageRepo+":") {
			continue
		}
		r, err := b.run(ctx, "image", "inspect", "--format",
			`{{.Size}}|{{.Created}}|{{index .Config.Labels "`+labelLayers+`"}}`, tag)
		if err != nil {
			return nil, err
		}
		if r.ExitCode != 0 {
			continue // removed while we looked
		}
		f := strings.SplitN(strings.TrimSpace(r.Stdout), "|", 3)
		if len(f) < 3 {
			continue
		}
		size, _ := strconv.ParseInt(f[0], 10, 64)
		at, _ := time.Parse(time.RFC3339Nano, f[1])
		im := runners.Image{Key: keyOf(tag), Tag: tag, OS: "linux", SizeMB: size / (1 << 20), CreatedAt: at}
		if f[2] != "" {
			im.Layers = strings.Split(f[2], ",")
		}
		out = append(out, im)
	}
	return out, nil
}

// PruneImages keeps the newest keep images and deletes older ones that no pool
// container uses (runners R5.33). It returns the keys it deleted.
func (b *Backend) PruneImages(ctx context.Context, keep int) ([]string, error) {
	imgs, err := b.ListImages(ctx)
	if err != nil {
		return nil, err
	}
	res, err := b.run(ctx, "ps", "-a", "--filter", "label="+LabelPool, "--format", `{{.Image}}`)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		// Fail closed: an image may be in use.
		return nil, fmt.Errorf("ps: %s", b.redact(strings.TrimSpace(res.Stderr)))
	}
	inUse := map[string]bool{}
	for _, tag := range strings.Fields(res.Stdout) {
		inUse[keyOf(tag)] = true
	}
	var gone []string
	for _, im := range runners.SelectPrune(imgs, inUse, keep) {
		r, err := b.run(ctx, "rmi", im.Tag)
		if err != nil {
			return gone, err
		}
		if r.ExitCode == 0 {
			gone = append(gone, im.Key)
		}
	}
	return gone, nil
}
