# OPS70 report: persistent container runner shows "its service is not on the machine"

## Causes
1. **Service discovery.** `LocalAgent.Found` asked only the OS (persistent) backend. The container backend never listed the runners of `crsp-persistent-pool`, so for a record with `ServiceName = crsp-persistent-pool:<name>` the view had `ServiceKnown && Service == nil` (`internal/runners/view.go`) and showed Stopped.
2. **Two folders.** `gameroompc-pool-1-r1378816711` and `...-r452891552` carry two different repository ids. A pool runner registers once per repository that uses the pool (`regName`, R5.88), so these are two registrations by design, not a double start. The real double-start risks found in `add-runner.sh`: a second add of one name ran `config.sh --replace` while the old listener still ran, and a failed retry deleted the folder of a runner already set up.

## Fix
- `images/persistent/list.sh` (new), `Backend.FindPersistent`, `ParsePersistentFound`: list the container's runners as `backend.Found` with the record's ref.
- `LocalAgent.Services` merges OS and container services; `linkAgent.Services` uses it. `ReportFound` still gets only the OS list, so no container runner is adopted as an OS runner.
- `view.go`: match by record ref, then by runner name (`nameKey`).
- `add-runner.sh`: idempotent per name (removes the entry, waits for the supervisor to end the listener, refuses if it is still up); a failed registration removes only a folder the call made.
- Removal already removes `.env`, `.stopped`, folder and deps (`cleanUpPersistent`).

## Tests (fakes, no sleeps, <= 20 cases)
container: TestParsePersistentFoundNamesTheRecordsRef, TestFindPersistentReadsTheContainerAndToleratesNoContainer. runners: TestContainerRunnerIsFoundByRecordOrName. images: TestAddRunnerTwiceLeavesOneFolderAndOneEntry, TestAddRunnerRetryAfterFailureKeepsTheSetUpFolder, TestAddRunnerRefusesWhileTheListenerStillRuns, TestListScriptNamesEntriesAndWhetherTheyRun.

## Docs
features/runners R5.124 + test row + change note; features/machines change note.

## Not verified
Real PC: not reproduced here; the two-registration reading comes from the folder ids. Two listeners under the same name were not seen in code beyond the add race above.

## Design-system checklist
No UI change; CORE rules and design-system not touched.
