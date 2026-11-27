package api

import (
	"context"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/api/gen"
	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/sizelabel"
)

type fakeSizeMachines struct{}

func (fakeSizeMachines) SizeLabelMachines(context.Context) ([]sizelabel.Machine, error) {
	return []sizelabel.Machine{{Name: "pc1", OS: "windows", CPUs: 8, MemoryGiB: 16}}, nil
}

func TestListSizeLabelsGivesLabelsExampleAndMachines(t *testing.T) {
	s := &server{opts: Options{SizeLabels: fakeSizeMachines{}}}
	res, err := s.ListSizeLabels(context.Background(), gen.ListSizeLabelsRequestObject{})
	if err != nil {
		t.Fatal(err)
	}
	body := gen.SizeLabelList(res.(gen.ListSizeLabels200JSONResponse))
	if len(body.Labels) != 2 || body.Labels[0].Key != "cpu" || body.Example != sizelabel.Example {
		t.Fatalf("labels %+v", body)
	}
	if len(body.Machines) != 1 || body.Machines[0].Name != "pc1" || body.Machines[0].Cpus != 8 {
		t.Fatalf("machines %+v", body.Machines)
	}
}
