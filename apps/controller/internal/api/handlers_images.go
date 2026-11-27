package api

import (
	"context"
	"net/http"
	"sort"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/audit"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/container"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/machines"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/repos"
)

// imagesOff is true until the images and caches area is wired, so the unwired
// routes answer 501 like every other unbuilt area (the same service group).
func (s *server) imagesOff() bool { return s.opts.Caches == nil }

// An image is per machine and OS; every repository on the machine uses it
// (runners R5.32). Its id is "img_<machine id>_<os>".
const imageIDPrefix = "img_"

func imageID(machineID, os string) string { return imageIDPrefix + machineID + "_" + os }

func splitImageID(id string) (machineID, os string, ok bool) {
	if !strings.HasPrefix(id, imageIDPrefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(id, imageIDPrefix)
	i := strings.LastIndex(rest, "_")
	if i <= 0 {
		return "", "", false
	}
	return rest[:i], rest[i+1:], true
}

// canRunContainers says whether a machine can run container jobs: Linux with a
// container engine the agent found and can use. Whether the user allowed the
// backend yet is a separate choice; the layers can be chosen before.
func canRunContainers(v machines.View) bool {
	if v.Machine.OS != "linux" {
		return false
	}
	for _, b := range v.Backends {
		if (b.Name == "docker" || b.Name == "podman") && b.Present && b.Usable {
			return true
		}
	}
	return false
}

func genTools(in []container.Tool) []gen.ImageTool {
	out := make([]gen.ImageTool, 0, len(in))
	for _, t := range in {
		g := gen.ImageTool{Name: t.Name}
		if t.Version != "" {
			v := t.Version
			g.Version = &v
		}
		out = append(out, g)
	}
	return out
}

// poolRepo is what the layer lists need of a repository.
type poolRepo struct {
	name     string
	labels   []string
	packages []string
}

// poolRepositories lists the repositories that use the pool. Pages of the
// store are read until the last.
func (s *server) poolRepositories(ctx context.Context) []poolRepo {
	a := s.ghArea()
	if a == nil || a.Repos == nil {
		return nil
	}
	p := s.principal(ctx)
	on := true
	var out []poolRepo
	after := ""
	for {
		rows, err := a.Repos.List(ctx, p, repos.ListFilter{Pool: &on, AfterName: after, Limit: 200})
		if err != nil || len(rows) == 0 {
			break
		}
		for _, r := range rows {
			out = append(out, poolRepo{name: r.FullName, labels: r.Policy.Labels, packages: r.Policy.ExtraPackages})
		}
		after = rows[len(rows)-1].FullName
		if len(rows) < 200 {
			break
		}
	}
	return out
}

// neededBy counts the repositories whose labels ask for the layer.
// TODO(LOOK12): `failing` stays 0 until a job failure that names a missing
// layer tool (clang-cl, wine, qemu) is cheap to read; see the open questions of
// the caches spec.
func neededBy(l container.Layer, rs []poolRepo) gen.LayerNeededBy {
	names := []string{}
	for _, r := range rs {
		if asks(r.labels, l.Labels) {
			names = append(names, r.name)
		}
	}
	sort.Strings(names)
	return gen.LayerNeededBy{Count: len(names), Repositories: names}
}

func asks(have, want []string) bool {
	for _, h := range have {
		for _, w := range want {
			if strings.EqualFold(h, w) {
				return true
			}
		}
	}
	return false
}

func (s *server) buildImage(ctx context.Context, v machines.View, rs []poolRepo) (gen.Image, error) {
	const os = "linux"
	var chosen []string
	if s.opts.Settings != nil {
		var err error
		if chosen, err = s.opts.Settings.GetImageLayers(ctx, v.Machine.ID, os); err != nil {
			return gen.Image{}, err
		}
	}
	on := map[string]bool{}
	for _, c := range chosen {
		on[c] = true
	}
	layers := []gen.ImageLayer{}
	for _, l := range container.OptionalLayers() {
		layers = append(layers, gen.ImageLayer{
			Id: l.Name, Name: l.Title, Enabled: on[l.Name], Purpose: l.Purpose,
			Adds: genTools(addsOf(l)), NeededBy: neededBy(l, rs),
		})
	}
	extras := []gen.ImageExtraPackages{}
	for _, r := range rs {
		if len(r.packages) > 0 {
			extras = append(extras, gen.ImageExtraPackages{Repository: r.name, Packages: r.packages})
		}
	}
	// The controller does not read the images a machine built yet (open
	// question 20), so each image shows as not built.
	return gen.Image{
		Id: imageID(v.Machine.ID, os), MachineId: v.Machine.ID, Os: gen.OS(os), Base: "ubuntu-24.04",
		BaseTools: genTools(container.BaseTools()), ExtraPackages: extras, Layers: layers,
		State: gen.ImageStateNotBuilt, Newest: true,
	}, nil
}

func addsOf(l container.Layer) []container.Tool {
	var out []container.Tool
	for _, n := range container.LayerAdds(l) {
		out = append(out, container.Tool{Name: n})
	}
	return out
}

// ListImages implements GET /api/v1/images: one image per machine and OS that
// can run container jobs, built or not.
func (s *server) ListImages(ctx context.Context, _ gen.ListImagesRequestObject) (gen.ListImagesResponseObject, error) {
	if s.imagesOff() {
		return gen.ListImagesdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	if s.opts.Machines == nil {
		return gen.ListImages200JSONResponse{Items: []gen.Image{}}, nil
	}
	ms, err := s.opts.Machines.List(ctx)
	if err != nil {
		s.opts.Logger.Error("images", "err", err)
		return gen.ListImagesdefaultJSONResponse{StatusCode: http.StatusInternalServerError, Body: errEnvelope(CodeInternal, "internal error")}, nil
	}
	rs := s.poolRepositories(ctx)
	items := []gen.Image{}
	for _, v := range ms {
		if !canRunContainers(v) {
			continue
		}
		img, err := s.buildImage(ctx, v, rs)
		if err != nil {
			s.opts.Logger.Error("images", "err", err)
			return gen.ListImagesdefaultJSONResponse{StatusCode: http.StatusInternalServerError, Body: errEnvelope(CodeInternal, "internal error")}, nil
		}
		items = append(items, img)
	}
	return gen.ListImages200JSONResponse{Items: items}, nil
}

func (s *server) findImage(ctx context.Context, id string) (gen.Image, string, string, *gen.ErrorEnvelope) {
	notFound := errEnvelope(CodeNotFound, "No such image.")
	machineID, os, ok := splitImageID(id)
	if !ok || os != "linux" || s.opts.Machines == nil {
		return gen.Image{}, "", "", &notFound
	}
	v, err := s.opts.Machines.Get(ctx, machineID)
	if err != nil || !canRunContainers(v) {
		return gen.Image{}, "", "", &notFound
	}
	img, err := s.buildImage(ctx, v, s.poolRepositories(ctx))
	if err != nil {
		e := errEnvelope(CodeInternal, "internal error")
		return gen.Image{}, "", "", &e
	}
	return img, machineID, os, nil
}

func imageFail(e gen.ErrorEnvelope) int {
	if e.Error.Code == CodeNotFound {
		return http.StatusNotFound
	}
	return http.StatusInternalServerError
}

// GetImage implements GET /api/v1/images/{imageId}.
func (s *server) GetImage(ctx context.Context, req gen.GetImageRequestObject) (gen.GetImageResponseObject, error) {
	if s.imagesOff() {
		return gen.GetImagedefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	img, _, _, e := s.findImage(ctx, req.ImageId)
	if e != nil {
		return gen.GetImagedefaultJSONResponse{StatusCode: imageFail(*e), Body: *e}, nil
	}
	return gen.GetImage200JSONResponse(img), nil
}

// UpdateImageLayers implements PUT /api/v1/images/{imageId}/layers: the choice
// is stored and used by the next build, also the first (runners R5.32).
func (s *server) UpdateImageLayers(ctx context.Context, req gen.UpdateImageLayersRequestObject) (gen.UpdateImageLayersResponseObject, error) {
	if s.imagesOff() {
		return gen.UpdateImageLayersdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	_, machineID, os, e := s.findImage(ctx, req.ImageId)
	if e != nil {
		return gen.UpdateImageLayersdefaultJSONResponse{StatusCode: imageFail(*e), Body: *e}, nil
	}
	if req.Body == nil {
		return gen.UpdateImageLayersdefaultJSONResponse{StatusCode: http.StatusBadRequest, Body: errEnvelope(CodeBadRequest, "enabled_layers is required")}, nil
	}
	known := map[string]bool{}
	for _, l := range container.OptionalLayers() {
		known[l.Name] = true
	}
	for _, n := range req.Body.EnabledLayers {
		if !known[n] {
			return gen.UpdateImageLayersdefaultJSONResponse{StatusCode: http.StatusBadRequest, Body: errEnvelope(CodeBadRequest, "unknown layer "+n)}, nil
		}
	}
	if s.opts.Settings == nil {
		return gen.UpdateImageLayersdefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	if _, err := s.opts.Settings.SetImageLayers(ctx, audit.ActorFrom(ctx), machineID, os, req.Body.EnabledLayers); err != nil {
		s.opts.Logger.Error("images", "err", err)
		return gen.UpdateImageLayersdefaultJSONResponse{StatusCode: http.StatusInternalServerError, Body: errEnvelope(CodeInternal, "internal error")}, nil
	}
	img, _, _, e := s.findImage(ctx, req.ImageId)
	if e != nil {
		return gen.UpdateImageLayersdefaultJSONResponse{StatusCode: imageFail(*e), Body: *e}, nil
	}
	return gen.UpdateImageLayers200JSONResponse(img), nil
}

// RebuildImage implements POST /api/v1/images/{imageId}/rebuild. An image that
// is not built has nothing to rebuild: the first runner builds it.
func (s *server) RebuildImage(ctx context.Context, req gen.RebuildImageRequestObject) (gen.RebuildImageResponseObject, error) {
	if s.imagesOff() {
		return gen.RebuildImagedefaultJSONResponse{StatusCode: http.StatusNotImplemented, Body: notImplemented()}, nil
	}
	_, _, _, e := s.findImage(ctx, req.ImageId)
	if e != nil {
		return gen.RebuildImagedefaultJSONResponse{StatusCode: imageFail(*e), Body: *e}, nil
	}
	return gen.RebuildImagedefaultJSONResponse{StatusCode: http.StatusConflict, Body: errEnvelope(CodeConflict, "The image is not built yet. It is built when the first runner starts.")}, nil
}
