---
title: Move one repository from the old pool to crsp, and back
type: how-to
---
# Move one repository from the old pool to crsp, and back

For the owner, on the Windows PC where both pools run. One repository, one operating system at a time. There is no gate and no soak: you move a repository, watch real runs, and use crsp on real work.

Rules that hold the whole time:

- The old pool is not deleted here. Removing it is the owner's choice, when the owner is satisfied.
- crsp touches only containers named `crsp-` and the variables of repositories turned on in it.
- Move one repository, then wait for a full CI run of a pull request and one run on the default branch before the next one.
- The way back is one variable. Do not remove the old runner until you are sure you do not need it.

## Before you start

1. `crsp doctor` shows no `warn` and no `fail`.
2. `crsp doctor --json` lists the repository's jobs for the OS as `covered` (doctor R5.33). A `missing` or `not checked` row means the repository stays on the old pool for that OS.
3. Write down the repository's current variable values: `CI_RUNS_ON` for Linux, `CI_RUNS_ON_WINDOWS` for Windows. This is the way back.
4. Note the job times of the last runs on the old pool, for the comparison in step 5 of the move.

## Move

1. Pick the OS (Linux or Windows) and the repository: the one with the fewest jobs first.
2. Turn the pool on for the repository in the console (or `crsp repo enable OWNER/NAME`). crsp installs its runners and writes `RUNS_ON_<OS>`. With the setting `legacy_vars` on it also writes `CI_RUNS_ON` or `CI_RUNS_ON_WINDOWS`, so the old templates follow. Without it, set both variables by hand.
3. Do not run the old pool's setup for this repository again. It would write the old labels back.
4. Leave the repository's old runner registered and idle.
5. Run the repository's CI on a pull request. Then run it on the default branch. Watch with `crsp job watch --repo OWNER/NAME`. Compare the job times with the ones you noted. A job that waits longer than before: look at it with `crsp job list --repo OWNER/NAME --state queued`, and in the console, where the wait reason is shown.
6. Lower `ci_slots` of the old pool by what moved, so the two pools do not double the slots on one PC.
7. Keep working on the repository as usual. When the runs stay green and the times are as good as before, the repository is done. The old runner goes away with the old pool, when the owner chooses.

If a run is red because of crsp, go back (next part) and tell the agent that works on crsp which job failed. Do not retry until it is fixed.

## Move back

Use this when a run fails because of crsp, a job waits too long, or the doctor shows `fail` for the repository.

1. Set the variable of that OS back to the value you wrote down. For Linux this is `CI_RUNS_ON`, for Windows `CI_RUNS_ON_WINDOWS`. Set it in the repository's variables on GitHub.
2. Turn the pool off for the repository (`crsp repo disable OWNER/NAME`), so crsp stops writing `RUNS_ON_<OS>` and `legacy_vars`. crsp names what it removes and asks first; running jobs finish (crsp never cuts a job).
3. Check that the old runner is online and idle on the repository's runner list on GitHub. If it is offline, start it with the old pool's own tools.
4. Raise `ci_slots` of the old pool by what the repository had.
5. Run the repository's CI on a pull request. It must run on the old runner.
6. Write down what failed, so it is fixed before the next try.

## What this runbook does not do

- It does not delete the old pool. That is the owner's choice.
- It has no parity check, no soak and no access for CI: those were removed (CRSP227).

## Clean-up of repositories set up earlier

A repository that was set up for the old parity check still holds the secret `CRSP_ACCESS` and the variables `CRSP_URL` and `CRSP_CA_FINGERPRINT`, and the workflow `parity.yml` if it was copied there. crsp no longer writes or reads them and does not remove them. Delete them by hand in the repository's settings on GitHub (Secrets and variables, Actions) when you want.
