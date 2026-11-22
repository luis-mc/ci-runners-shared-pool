---
title: Real-machine test pass
type: guide
---

# Real-machine test pass

The owner runs this once on a Windows PC and once on a Linux PC after installing a release (owner decision D107). Tests that run in CI cannot say whether a real install looks right on a real desk, so this pass does. Plan about 60 minutes for each PC. It started as the test pass for v0.1.14.

Each step says what to do in the console, what you should see, and what to report when you do not. The words in **bold** are the words on the screen (design-system `content`). Use a terminal only where a step says so.

## Before you start

- [ ] Open the release page: <https://github.com/luis-mc/ci-runners-shared-pool/releases>. Pick the newest `v*` release (not `edge`).
- [ ] Download the installer for this PC and `SHA256SUMS` into one folder. Windows: `crsp-windows-amd64.msi` (`arm64` on an ARM PC). Linux: `crsp-linux-amd64.deb` and `crsp-linux-amd64.tar.gz` (`arm64` on an ARM PC). Check the files as in [Install and check a download](install.md).
- [ ] Pick one **private** repository to test with. Public repositories stay on GitHub-hosted runners unless public repositories are turned on in Settings.
- [ ] Keep `scripts/real-machine/check.ps1` (Windows) or `check.sh` (Linux) from this repository at hand. Step 14 uses it, and so does every failure report.

### One-time manual install note

From the first release with versioned installer names (OPS40; for example `crsp-windows-amd64_0.1.15.msi`), a machine on **0.1.14 or older** cannot update itself to that release, because the old updater looks for the old asset names. Install that release **by hand once** (step 1 below, over the old version; the data stays). After that the machine updates itself as usual. Say so in your report if a machine on 0.1.14 shows no update, or the update fails to find its file.

## How to report a failure

Write the step number, what you saw (a screenshot of the console screen helps), and what you expected. Then run the check script, which collects read-only facts into one text file, and attach that file. Do not edit the file: it holds config key names only, never values, no tokens and no keys.

- Windows (PowerShell, as administrator): `powershell -ExecutionPolicy Bypass -File scripts\real-machine\check.ps1`
- Linux: `sudo bash scripts/real-machine/check.sh`

The script prints `Wrote <file>` with the file name. It changes nothing on the machine.

## 1. Install

- [ ] **Windows, MSI.** Double-click the MSI. SmartScreen warns, because the installers are not signed: choose **More info**, then **Run anyway**. The installer asks for administrator rights once and ends with no other question.
  - See: in **Services**, `crsp` (CI Runners Shared Pool) is **Running** and **Automatic**. The Start Menu has **CI Runners Shared Pool**.
- [ ] **Linux, deb.** Open the file in the software installer, or run `sudo apt install ./crsp-linux-amd64.deb` (a terminal is unavoidable here: there is no console yet).
  - See: `systemctl status crsp` says `active (running)`.
- [ ] **Linux, tar.gz.** Run `install.sh` from the release (R5.1), which checks the archive and installs the service. This is the one install path that has no `install-kind` marker; the updater treats it as a tarball install.
  - See: the same `active (running)`.
- [ ] Over an older install (an upgrade): the data stays, the service stops and starts again, and the console shows your machines and runners as before.
- Report if: an install asks more than one question, the service is not running, or an upgrade lost the machines.

## 2. Tray is up

- [ ] The tray icon shows. On Windows it comes back after you sign out and sign in. On Linux it shows in a desktop with a tray host; on a headless server there is no tray, and that is expected (note it).
- [ ] The tray menu header shows the product name, the version and the status. **Open** opens the console. **Check for updates** says you have the newest version.
- Report if: no icon, a wrong version in the header, or **Open** does nothing.

## 3. The console opens

- [ ] Choose **Open** in the tray menu. The browser opens the console. On a fresh install it opens the setup page through a one-time setup link.
- [ ] Not fresh: it opens the sign-in page, and **Sign in with GitHub** brings you to **Overview**.
- Report if: a blank page, a certificate wall you cannot pass, or a page that says how to get a setup link while you came from the tray (it says: choose **Open** in the tray menu).

## 4. Setup wizard (fresh install only)

The setup page shows three wizards in order. A later one stays greyed until the one before it is done.

- [ ] **Connect GitHub.** Choose your personal account, keep the App name, go to **Review**. It says "Nothing has changed yet." Go on: **Creating the App** sends you to GitHub and back. **Done** shows the App's name, its permissions and **Sign in with GitHub**.
- [ ] **Add the first machine.** Choose the OS of this PC. If this PC is the controller's own machine, the step says "This PC, {host name}, is in the pool." as done.
- [ ] **Turn on the first repository.** Pick only the test private repository. **Review** lists the variables to write. **Done** shows the repository turned on and its `CRSP_RUNS_ON_*` variables written.
- [ ] Reload the page at the end of each wizard: it shows **Done** again and not a blank step. A step you have not reached cannot be pressed.
- Report if: a button that starts work stays pressable while the work runs, a later step can be pressed early, **Stop** is missing while the wizard waits, or **Done** shows the plan and not what happened.

## 5. Add a machine

Do this on a second PC (the Windows PC when you test Linux, and the reverse) or skip it when you have one PC.

- [ ] **Shared Pool > Machines > Add a machine.** Choose the OS. **Review** shows the install command in a block with **Copy**, and how long the join code lasts.
- [ ] Run the command on the other PC (a terminal is unavoidable here). **Joining** waits for the agent, then **Done** shows the machine's name, OS, CPU, memory and backends.
- [ ] The machine shows in **Machines** as taking jobs (**Taking jobs**).
- Report if: the code runs out before the machine joins, the machine joins but shows no backends, or the command fails (attach its output).

## 6. Add runners

- [ ] **Shared Pool > Runners > Add runners.** The page says "Runners serve every repository that uses the pool." Choose System, Machine, Kind, Number and Labels.
- [ ] **Linux containers** (Linux PC, or a Windows PC with Docker): choose Linux and **Ephemeral** or **Persistent**. On Windows without the container backend, **Next** is disabled and the page says "The container backend is not allowed on this machine. Linux runners here need it." Use **Allow it on the machine**, then go on.
- [ ] **Windows runners** (Windows PC): choose Windows. Only **Persistent** is offered ("Ephemeral runners are not available for Windows yet. Windows runners are persistent.").
- [ ] **Review**, then the work, then **Done**. In **Runners** every runner shows online. In GitHub (repository > Settings > Actions > Runners) each shows **Idle**.
- [ ] Linux containers: the first runner builds the image; **Shared Pool > Images** shows it as built (**Built {time} · {size}**).
- Report if: a runner shows failed (the State column says why), a runner is online in the console and offline on GitHub, or an image never builds.

## 7. A repository opts in

- [ ] **Repositories**: tick the test repository, then **Use pool**. The wizard **Turn on the pool** has **Review**, **Turning on** and **Done**. Done shows the repository's variables written.
- [ ] Turning the pool on makes no light runner. Light jobs (smoke, prune, cleanup) run on the pool runners and take a light slot of the machine. After an upgrade from an older version no `crsp-light-*` row is left in **Runners**.
- [ ] In GitHub the repository has the `CRSP_RUNS_ON_*` variables.
- Report if: the repository stays listed as off, a public repository is allowed while public repositories are off, or a variable is missing in GitHub.

## 8. A job runs on the pool

- [ ] Push a workflow to the test repository with `runs-on: ${{ fromJSON(vars.CRSP_<JOB> || vars.CRSP_RUNS_ON_LINUX || '"ubuntu-latest"') }}` (`CRSP_RUNS_ON_WINDOWS` on Windows; `CRSP_<JOB>` is the per-job variable, upper case, and may be unset), or start one by hand. Make it last about two minutes (a `sleep 120` step) so you can watch it.
- [ ] The job runs on this PC and ends green on GitHub.
- Report if: the job waits more than a minute with a free runner (Doctor then says why, step 11), or fails with an error that names crsp.

## 9. The Jobs section shows it

Watch while the step 8 job runs. Open **Jobs**.

- [ ] **Queue**: a queued job shows its **Order**, **Why it waits** and **Starts** (for example "starts in about 4 to 7 min, 3rd in line" or "not enough history yet"). It leaves the queue when a runner takes it.
- [ ] **Running**: the job shows its **Workflow and job**, **Branch**, **Runner and machine** and **Progress**.
- [ ] **Done**: the finished job shows its result with a word, a glyph and a colour, and **Ran**.
- [ ] Open the row, then **Open run page**: the run page shows **Job graph** and **Timeline**. **Open the run on GitHub** opens the right run.
- [ ] The counts in **Overview** agree with **Jobs** (running now, done).
- Report if: the job is in two lists at once, never leaves **Queue**, the run page is empty, or a number disagrees with GitHub.

## 10. Doctor

- [ ] Open **Doctor**, then **Run checks** (after the first run it says **Run checks again**). The button is busy while it runs and says **Running the doctor**.
- [ ] Every check ends `ok`, or a `warn` or `fail` with a reason in plain words and a next action.
- [ ] **Runner leftovers.** The finding `Runner leftovers on {machine}` is `ok` when nothing is left over. If the machine ran runners before crsp (an old pool, a runner you removed by hand), it lists each item as **Service**, **Folder**, **Account** or **Download** with its name and size. No leftover on a clean PC is a pass; say so in the report and skip the next two lines.
- [ ] **Remove** opens the card **Remove these leftovers from {machine}?** It says "Each item below is removed from this machine. This cannot be undone." Press **Remove**: each item shows **Removed**. Run the checks again: the finding is gone.
- [ ] Without administrator rights on the machine the agent still lists leftovers but **Remove** is disabled, with the reason "The agent has no administrator rights. Run it as a service with rights."
- Report if: a leftover is listed that a live runner owns (it must never be), an item shows **Refused** or **Failed** (attach the reason), or a leftover stays after **Removed**.

## 11. Update check

- [ ] **Settings > Updates** (or the tray's **Check for updates**) shows the current version. **Look for updates** only looks: it never installs by itself.
- [ ] With a newer stable release available, the update dialog asks first: **Install now** or **Later**, with **What changed** as a link. **Later** changes nothing.
- [ ] **Automatic updates** is on by default. Switch it off and on again; the tray menu's tick follows.
- [ ] If you test an update: choose **Install now**. The console says what it waits for (running jobs). After the restart the console shows the new version, and the tray shows "Updated to {version}". Attach `result.json` and `stage.json` from the check script when it fails: they say which stage failed and the log path.
- Report if: **Look for updates** installs without asking, the dialog names the wrong version, or the update ends in `update failed`.

## 12. Restart the service

- [ ] Choose **Restart the service** (the tray menu has it; the console asks first in its Review step). Press it. A question card names the running jobs it waits for; nothing happens until you confirm.
- [ ] The console shows it waits, then the service comes back by itself, and the console reconnects with the same version and your machines and runners still there. A job that was running finishes first and is not cut.
- [ ] After a restart the tray shows **Running** again.
- Report if: the console does not come back within two minutes, or a running job is cut.

## 13. Uninstall leaves nothing

Do this last: it removes the install.

- [ ] First, in the console, remove the runners you added (**Shared Pool > Runners**), or let the uninstall drain them. In GitHub the runners are gone after the uninstall.
- [ ] **Windows:** **Settings > Apps**, uninstall **CI Runners Shared Pool**. The box **Also remove settings and data** is unticked by default. Run it once unticked (data stays in `%ProgramData%\crsp`), and, if you reinstall, once ticked.
- [ ] **Linux:** `sudo apt remove crsp` keeps the data; `sudo apt purge crsp` also deletes `/var/lib/crsp` and `/etc/crsp`. For a tar.gz install run `sudo crsp uninstall` (it lists what it removes and what it keeps, then asks).
- [ ] After the uninstall: no `crsp` service, no `actions.runner.*` service, no tray start entry, no firewall rule (Windows), no `crsp-pool:*` image you did not mean to keep (`docker image ls crsp-pool`), and the data folder only if you kept it.
- [ ] Run the check script once more. Its last section, **leftovers after an uninstall**, must show nothing present (apart from the data folder you kept).
- Report if: the check script lists a service, a folder, an account or a firewall rule that crsp made.

## 14. Collect the facts

Run the check script once after the install (step 3) and once after the last step, and keep both files. It collects:

| Section | What it reads |
|---|---|
| installed binary | `crsp version`, the install kind |
| version from `/healthz` | the version the running controller says, at `127.0.0.1` and the listen port from the config (8460 when not set) |
| service state | the `crsp` service state and start mode |
| runner services | every `actions.runner.*` service |
| docker images | `crsp-pool:*` images and `crsp-*` containers |
| data folder | size of each part, free space |
| update | the last `result.json` and `stage.json` of the update folder |
| config | **key names only**, never values |
| leftovers | what is still on the machine after an uninstall |

## Result

- [ ] Every box above is ticked on both PCs. Tell the orchestrator "real-machine pass ok" with the version.
- [ ] Or a step failed. Tell the orchestrator the step, what you saw, and attach the check files.

Specs: operations (install, config, self-update, uninstall), doctor R5.48 (runner leftovers), web-console 4.6 (Jobs).
