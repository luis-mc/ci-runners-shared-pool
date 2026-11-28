"""CRSP397: the gate's e2e step is the step named e2e, even when another ui-changed step comes first."""
import sys
from pathlib import Path

sys.dont_write_bytecode = True
sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "delivery"))

import delivery_common as dc  # noqa: E402


def test_named_e2e_step_wins_over_an_earlier_ui_changed_step():
    cfg = {"steps": [{"name": "lint-web", "scope": "ui-changed"}, {"name": "unit", "scope": "changed"},
                     {"name": "e2e", "scope": "ui-changed"}]}
    assert dc.e2e_step(cfg)["name"] == "e2e"


def test_first_ui_changed_step_is_used_when_none_is_named_e2e():
    cfg = {"steps": [{"name": "browser", "scope": "ui-changed"}, {"name": "x", "scope": "ui-changed"}]}
    assert dc.e2e_step(cfg)["name"] == "browser"


def test_no_ui_changed_step_gives_none():
    assert dc.e2e_step({"steps": [{"name": "unit", "scope": "changed"}]}) is None


def test_this_repository_gate_json_picks_e2e():
    import json
    cfg = json.loads((Path(__file__).resolve().parents[1] / "delivery" / "gate.json").read_text(encoding="utf-8"))
    assert dc.e2e_step(cfg)["name"] == "e2e"


def test_a_pool_screen_change_selects_its_specs_not_the_whole_suite():
    """CRSP401: apps/controller-console/src/screens/pool/ is claimed by specs in e2e_map, so a change there runs
    those specs ("affected") and the local smoke gate does not skip the e2e step."""
    import json
    cfg = json.loads((Path(__file__).resolve().parents[1] / "delivery" / "gate.json").read_text(encoding="utf-8"))
    claimed = {e["spec"] for e in cfg["e2e_map"]["specs"]
               if "apps/controller-console/src/screens/pool/" in e["paths"]}
    assert claimed == {
        "e2e/tests/web-console/pool-jobs.spec.ts",
        "e2e/tests/web-console/ux-jobs-rows.spec.ts",
        "e2e/tests/web-console/ux-queue-rows.spec.ts",
        "e2e/tests/design-layout.spec.ts",
    }
    # queue-fixtures.ts is not a full_on path (fixtures.ts is)
    assert "e2e/tests/web-console/queue-fixtures.ts" not in cfg["e2e_map"]["full_on"]


def _ctx(files, local_gate="smoke"):
    import json
    root = Path(__file__).resolve().parents[2]
    cfg = json.loads((root / "scripts" / "delivery" / "gate.json").read_text(encoding="utf-8"))
    cfg["local_gate"] = local_gate
    return dc.Context(root=root, cfg=cfg, base_ref="origin/main", base="b", head="h", files=files, packages=[],
                      changed_dirs=[], affected_dirs=[], widened=False)


def test_smoke_gate_with_a_full_selection_runs_the_changed_specs():
    """CRSP401: a change under packages/ui/ (full_on) selects 'full'; the smoke gate leaves the full suite to CI but
    runs the changed spec files instead of skipping the step."""
    spec = "e2e/tests/web-console/pool-machines.spec.ts"
    ctx = _ctx(["packages/ui/src/components.css", spec])
    assert dc.e2e_selection(ctx)[0] == "full"
    run, cmd, why = dc.plan_step(dc.e2e_step(ctx.cfg), ctx)
    assert run and spec in cmd and "full suite left to CI" in why


def test_smoke_gate_with_a_full_selection_and_no_spec_still_skips():
    ctx = _ctx(["packages/ui/src/components.css"])
    run, cmd, why = dc.plan_step(dc.e2e_step(ctx.cfg), ctx)
    assert not run and "CI is the one full run" in why


def test_smoke_gate_also_runs_the_specs_the_map_claims_for_a_changed_file():
    ctx = _ctx(["packages/ui/src/components.css", "apps/controller-console/src/screens/pool/QueuePlan.tsx"])
    run, cmd, why = dc.plan_step(dc.e2e_step(ctx.cfg), ctx)
    assert run and "ux-queue-rows.spec.ts" in cmd and "full suite left to CI" in why
