import json
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import rerun_plan  # noqa: E402

FILES = ["apps/controller/internal/agent/gate.go", "apps/controller/internal/agent/gate_test.go", "apps/controller/cmd/x/a_test.go", "go.mod"]
REPORT = "\n".join(json.dumps(e) for e in (
    {"Action": "fail", "Package": "mod/internal/api", "Test": "TestA"},
    {"Action": "fail", "Package": "mod/internal/api"},
))
RED = {"conclusion": "failure", "report": REPORT, "log": None}


def test_narrow_failed_plus_changed_package_on_shard_1():
    r = rerun_plan.plan(1, RED, ["apps/controller/internal/agent/gate.go"], FILES)
    assert r["mode"] == "narrow"
    assert ["mod/internal/api", "-run", "^(TestA)$"] in r["invocations"]
    assert ["./apps/controller/internal/agent"] in r["invocations"]


def test_other_shards_skip_changed_packages():
    r = rerun_plan.plan(2, RED, ["apps/controller/internal/agent/gate.go"], FILES)
    assert r["invocations"] == [["mod/internal/api", "-run", "^(TestA)$"]]


def test_valve_runs_whole():
    r = rerun_plan.plan(1, RED, ["go.mod"], FILES)
    assert r["mode"] == "whole"


def test_unmapped_change_runs_whole():
    r = rerun_plan.plan(1, RED, ["apps/controller-console/src/a.ts"], FILES)
    assert r["mode"] == "whole"


def test_green_previous_is_current():
    r = rerun_plan.plan(1, {"conclusion": "success"}, ["apps/controller/internal/agent/gate.go"], FILES)
    assert r["mode"] == "current"


def test_unreadable_report_runs_whole():
    r = rerun_plan.plan(1, {"conclusion": "failure", "report": None, "log": None}, ["apps/controller/internal/agent/gate.go"], FILES)
    assert r["mode"] == "whole"
