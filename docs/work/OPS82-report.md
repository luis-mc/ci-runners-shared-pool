# OPS82 report: per-test timeouts (G1)

- conftest.py:1 (pytest, no timeout): added `pytest.ini` (`timeout = 10`) and `scripts/ci/requirements-test.txt` (pytest 8.3.4, pytest-timeout 2.3.1, pinned; the repo had no Python dependency file). The ci.yml step "CI scripts unit tests" installs it and runs `python3 -m pytest scripts/ci`. 33 tests pass.
- ci.yml:302 (`go test` without -timeout): the guard matched the job display name `go test (${{ matrix.os }})`, not a command. Renamed to `Go tests (...)` (doctor spec R5.42 updated). Note for the owner: if branch protection requires the check names `go test (windows-latest)` / `go test (macos-latest)`, update them.
- go-test.sh:40 (150 s): `-timeout` limits one package binary. Packages over 60 s (agent, api, notify, scheduler, settings, store, packaging/hooks) now run in chunks (`chunks` in scripts/ci/go-tiers.json, `go-tiers.mjs chunks`), each chunk a `-run` of a share of the tests with `-timeout 60s`. No limit raised. Local -race run on 4 cores: all 3146 tests pass in 173 s.
- Slowest unit-tier tests: 5.1 s (agent TestAcceptingReportAndCPUCapOverTheLink), 3.3 s, 3.0 s. None over 10 s; no test moved.
- Guard: G1 PASS. G4 (idle_cost_unix_test.go:42 sleep) is not in scope.
- No test files of other agents touched.
- CI: ci.yml run 44972454678 on 1a7d4eb green (first run failed prettier on .pytest_cache; fixed with -p no:cacheprovider).
CODE DONE 1a7d4eb320bfcb15afcee0027b7753e8cccea559
