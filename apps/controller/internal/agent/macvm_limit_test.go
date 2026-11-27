package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/backend/macvm"
	agentv1 "github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/gen/agent/v1"
)

func TestMachineReportCarriesMacVMCount(t *testing.T) {
	rep := &agentv1.MachineReport{}
	addMacVMs(context.Background(), rep, func(context.Context) (macvm.Report, error) {
		return macvm.Report{Known: true, Count: macvm.Count{Running: 2, Ours: 1, Limit: 2}}, nil
	})
	m := rep.GetMacosVms()
	if !m.GetKnown() || m.GetRunning() != 2 || m.GetOurs() != 1 || m.GetLimit() != 2 {
		t.Fatalf("%v", m)
	}
}

func TestMacVMCountUnknownIsNotZero(t *testing.T) {
	rep := &agentv1.MachineReport{}
	addMacVMs(context.Background(), rep, func(context.Context) (macvm.Report, error) {
		return macvm.Report{}, errors.New("pgrep broke")
	})
	if m := rep.GetMacosVms(); m == nil || m.GetKnown() {
		t.Fatalf("a count that failed must say unknown: %v", m)
	}
}

func TestMacVMIntelReportsReason(t *testing.T) {
	rep := &agentv1.MachineReport{}
	addMacVMs(context.Background(), rep, func(context.Context) (macvm.Report, error) {
		return macvm.Report{Known: true, Unusable: "Intel", Count: macvm.Count{Limit: 2}}, nil
	})
	if rep.GetMacosVms().GetUnusableReason() != "Intel" {
		t.Fatal("reason lost")
	}
}

func TestNoMacVMSourceReportsNothing(t *testing.T) {
	rep := &agentv1.MachineReport{}
	addMacVMs(context.Background(), rep, nil)
	if rep.MacosVms != nil {
		t.Fatal("other OSes carry no macOS VM report")
	}
}
