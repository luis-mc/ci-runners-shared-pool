import json
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import rerun_plan  # noqa: E402

FILES = ["internal/agent/gate.go", "internal/agent/gate_test.go", "cmd/x/a_test.go", "go.mod"]
REPORT = "\n".join(json.dumps(e) for e in (
    {"Action": "fail", "Package": "mod/internal/api", "Test": "TestA"},
    {"Action": "fail", "Package": "mod/internal/api"},
))
RED = {"conclusion": "failure", "report": REPORT, "log": None}


def test_narrow_failed_plus_changed_package_on_shard_1():
    r = rerun_plan.plan(1, RED, ["internal/agent/gate.go"], FILES)
    assert r["mode"] == "narrow"
    assert ["mod/internal/api", "-run", "^(TestA)$"] in r["invocations"]
    assert ["./internal/agent"] in r["invocations"]


def test_other_shards_skip_changed_packages():
    r = rerun_plan.plan(2, RED, ["internal/agent/gate.go"], FILES)
    assert r["invocations"] == [["mod/internal/api", "-run", "^(TestA)$"]]


def test_valve_runs_whole():
    r = rerun_plan.plan(1, RED, ["go.mod"], FILES)
    assert r["mode"] == "whole"


def test_unmapped_change_runs_whole():
    r = rerun_plan.plan(1, RED, ["apps/web/src/a.ts"], FILES)
    assert r["mode"] == "whole"


def test_green_previous_is_current():
    r = rerun_plan.plan(1, {"conclusion": "success"}, ["internal/agent/gate.go"], FILES)
    assert r["mode"] == "current"


def test_unreadable_report_runs_whole():
    r = rerun_plan.plan(1, {"conclusion": "failure", "report": None, "log": None}, ["internal/agent/gate.go"], FILES)
    assert r["mode"] == "whole"
