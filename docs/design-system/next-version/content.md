# Content: the words users see

> **Starter.** Every item in this file is a starter item. Keep, change or delete each one before v1 is approved.

Labels, messages and vocabulary live here (DOCSPEC R11.3). One word per action, in every app (UX-1).

## Action vocabulary

Each action has one word. The Never use column lists words that must not stand in for it.

| Word | Meaning | Never use |
|---|---|---|
| Save | Keep the changes made in a form. | Submit, Apply, Update |
| Cancel | Leave a form or question without doing anything. | Close, Back out, Discard |
| Add | Put a new item in a list. | Create, New, Insert |
| Edit | Change an item that already exists. | Modify, Change |
| Remove | Take an item out of a list. The item itself stays. | Delete, Clear |
| Delete | Destroy the item for good. | Erase, Trash, Remove |
| Clear | Empty a selection or a filter. | Reset, Deselect |
| Select all N | Select every item in the list. N is the count. | Check all, Pick all |
| Stop | End work that is running. | Cancel, Abort, Kill |
| Try again | Repeat something that failed. Also asks once more for a state. | Retry, Redo, Check again, Check now, Recheck |
| Refresh | Load the shown data again. | Reload, Update |
| Review | Open what waits for a decision, to look it over. Also the name of every looking-over step, and of the first of the last three steps of a wizard that changes something. | Check, Confirm, Summary |
| Hide | Stop showing something. It is kept. Also the x on a note. | Dismiss, Close |
| Show | Show something that was hidden. | Reveal, Unhide |
| Open | Go to an item to see it. | View, Go to |
| See all N | Go from a preview to the whole list. N is the count. | More, View all |
| Back | Go to the step before. | Previous |
| Next | Go to the step after, when it is not the last step. | Continue, Forward |
| Done | The last step of a wizard: what really happened, never the plan. Its one primary is the next action. | Finish, Finished, Complete, Result, OK |
| Search | Look for text in a list. | Find, Look up |
| Sort | Order a list by a column. | Order, Arrange |
| Sign in with GitHub | Start the GitHub sign-in. | Log in, Login |
| Sign in with GitHub again | The action of the needs-you message when the GitHub sign-in ended; starts the GitHub sign-in and returns to the same screen. | Log in again, Reconnect |
| Sign out | End the session. | Log out, Logout |
| Stop new jobs | Stop a machine taking new jobs; the running ones finish. A secondary button, with the hint line "Running jobs finish first. Resume takes jobs again." Question card title "Stop new jobs on {machine}?"; busy word Stopping new jobs. The API and CLI keep the name drain. | Drain, Pause, Disable |
| Resume | Let a machine that stopped new jobs take jobs again. | Enable, Undrain |
| Turn on | Let a repository use the pool. | Enable, Activate |
| Turn off | Stop a repository using the pool. | Disable, Deactivate |
| Copy | Copy a command or a value. It changes nothing. | Clipboard |
| Look for updates | Ask GitHub for a newer stable release now. It only looks: it never installs by itself, with Automatic updates on or off. A newer release opens the update dialog, which asks first. | Check for updates, Refresh |
| Update to N | Install release N after the running jobs end. N is the version. | Upgrade, Install |
| Restart the service | Stop and start the service after the running jobs end. | Reboot, Reset |
| Automatic updates | The switch that lets the regular check install a newer stable release by itself after the running jobs end. A look a person asks for never installs by itself. | Auto-upgrade |
| Install now | The main button of the update dialog, on the right. Installs the newer release after the running jobs end; it stops none. | Update now, Upgrade, Yes |
| Later | The other button of the update dialog, on the left. Changes nothing. Closing the dialog is Later. | Not now, Skip, No |
| What changed | The link of the update dialog to the release page of the new version. It changes nothing. | Release notes, Changelog |
| Quit tray | Close the tray icon only. The service keeps running. | Exit, Close |
| Export | Save every row of a table or list as a file, in the order shown (component 29). It changes nothing. | Download, Save as |
| Run checks again | Start the doctor now, on the Doctor page, as its one primary in the action bar. Before the first run it says Run checks. While it runs the button says Running the doctor. | Run the doctor, Scan, Check |
| Add channel | Open the form of a new notification channel. It changes nothing until the tab's Save. A normal button. | Add, New channel, Create |
| Widen the range | Show a longer time range when the range shown has nothing. | Expand, Zoom |
| Zoom in, Zoom out | Show a shorter or a longer time range on a time axis. | Narrow, Expand |
| Show earlier | Load the rows before the last one on a time axis. It is also loaded by itself as the end nears the screen. | More, Older |
| Wipe | Empty one cache of everything it holds, after the running jobs that use it end. The cache stays. | Clear, Purge, Empty, Flush |
| Rebuild | Build an image again after the running jobs end. The old image serves until the new one is ready. | Recreate, Refresh, Reinstall |
| Export config | Save the configuration as a file, without any secret. | Download config, Backup config |
| Exporting | The busy word of Export, while the controller builds the file. | Downloading |
| Load more | Read the next rows of a list that has more on the controller. | Show more, Next page |
| Send test | Send one clearly marked test notice through a channel and say what happened. It changes nothing else. | Test, Ping, Verify, Send a test |
| Pause for 1 hour, Pause until I resume | Hold a machine from taking new jobs for an hour, or until Resume. The running jobs finish. | Stop, Suspend, Disable |
| Resume | End a pause, and let the machine take jobs again. The machine page and the tray use it. | Unpause, Restart, Enable |

Add a row for each action the project needs. A new action gets a word that is not used above.

## Retired words

A retired word is not drawn as a label, button or message. The guard scans the source for each one. Do not list an approved word here.

<!-- retired-words:start -->
| Retired | Use instead |
|---|---|
| Submit | Save |
| Click here | (name the action) |
| Retry | Try again |
| Reload | Refresh |
| Abort | Stop |
| Dismiss | Hide |
| Check | Review (to look over, or to open what waits), or Try again (to ask once more for a state) |
| Check again | Try again |
| Check now | Try again |
| Recheck | Try again |
| Erase | Delete |
| Runner kind | Runner type |
| Kind (for persistent or ephemeral) | Runner type; Type alone where the page or step is already about runners (LOOK50) |
<!-- retired-words:end -->

## Wizard step names

A wizard that changes something ends with three steps (UX-30).

| Step | Name | Shows |
|---|---|---|
| First of the last three | Review | What will happen, in numbers and words. "Nothing has changed yet." |
| Second | The work, in the "-ing" form | Live progress: "Moving 3 of 12". |
| Third | Done | What really happened, what was left, and what failed. |

A step that only reads before Review is named by what it reads ("Reading files", "Reading folders"). It changes nothing. It is never called "Checking". The wait for the data of the current step is one line of the same words ("Reading machines"), shown in that step and beside its locked primary; it is not a step in the step row.

## Wizard state words

Each word is used for one thing in a wizard (UX-30).

| Word | When to use it |
|---|---|
| Stop | Work is running and the user ends it. |
| Stopping | Stop was pressed and the work has not ended yet. |
| Pause | The work holds its place and can go on later. |
| Try again | A failed action is repeated. It repeats that action only. |
| Start again | The wizard has ended (Done or Stopped) and the user begins a new run. |
| Put back | Undo what the run changed, when that can be undone. |

## App health and left-installation words

The words of the App-gone banner on the Overview (github-integration R5.6) and the reason a repository left the installation (R5.22).

| Where | Word |
|---|---|
| Banner (problem) | The GitHub App is gone. {reason} crsp has stopped its GitHub work. Jobs that are running keep running. |
| Banner action | Run the setup wizard again |
| Reasons | GitHub does not know this App any more. / GitHub refused the App's key. / The App's key is not in this machine's secret store. |
| List word | left the installation (in the Last delivery column of a repository the controller turned off) |
| Repository fact | Why it is off: This repository left the GitHub App's installation, so its pool was turned off. |

## Message wording

A message is one or two short sentences in plain words. It names the thing, never a code or a path.

| Tone | Pattern | Example |
|---|---|---|
| Done | What was done, in the past tense. | Settings saved. |
| Note | A fact the user may want to know. No action is needed. | 3 items are hidden by the filter. |
| Needs you | What is waiting, and what the user can do. | The report needs a title. Add a title to go on. |
| Problem | What failed, why, and what next. | Could not save. The connection was lost. Reconnect, then try again. |

- Start with the subject, not with "Error" or "Oops".
- Say what happened first, then what to do.
- Use the same word for the same thing every time.
- Never blame the user.

## Audit log words

| Where | Word |
|---|---|
| Column heads | When, Who, What, On, Details, Result |
| On | What the change acted on: its kind, a space, its name. "repository luis-mc/app". |
| Details | The note of the change as plain text, with no key name before it. |
| Export | The word Export, at the end of the toolbar. |

## Empty, failed and waiting states

- **Empty:** say what will appear here, and offer the action that fills it. "No reports yet. Add a report to start."
- **Failed:** say what failed, why, and what next.
- **Waiting:** name what is happening. "Loading 3 of 12 reports."

## Dates, times and numbers

| Kind | Format | Example |
|---|---|---|
| Date | Year-month-day. | 2026-11-02 |
| Time | 24-hour, local time. | 14:30 |
| Date and time | Date, a space, then time. Always on one line (UX-78). | 2026-11-02 14:30 |
| Short date and time | Day, month name, time; only where a cell is narrow (UX-72). The detail shows the full form above. | 18 Nov 13:28 |
| Number | Digits, with a space for thousands. A dot for decimals. | 12 345.5 |
| Count | A number before the noun. The noun agrees with the count. | 1 item, 3 items |
| Size | A number and a unit, with a space. | 4.2 MB |
| Percent | A number and the percent sign, no space. | 45% |
| Full stamp | Date and time with seconds. Only in logs and exact moments. | 2026-11-02 14:30:05 |
| File stamp | Date and time for a file name, joined by dashes. | 2026-11-02_14-30-05 |
| Past moment | The exact time first, then how long ago. | 2026-11-02 14:30 (5 h ago) |
| Time left | Short words, never seconds. | about 2 min left |
| Sentence | Minutes only, and the time zone is never named. | Published 2026-11-02 14:30. |
| Duration | Short words, seconds kept, never negative. | 45 s, 4 min 12 s, 1 h 5 min |

## Images and caches words

The words of the images and caches screen (web-console 4.7).

| Where | Word |
|---|---|
| Tabs | Images, Caches |
| Cache kinds | Tool cache, Rust build cache, Actions cache, Git mirror, Registry mirror, Dependency cache |
| Empty images | No images yet. A machine shows one here once it can run container jobs. |
| Empty caches | No caches yet. Caches show here when a runner has been added. |
| Question title | Wipe {cache}? Rebuild the image of {machine}, {OS}? |
| Wiping | Wiping {cache}, waiting for {jobs} |
| Last wiped, none | never |
| No cap | {size}, no cap |
| Over the cap | {size} of {cap} ({percent}), over the cap |
| Wipe failed | Could not wipe {cache}. {why} |
| Rebuild failed | Could not rebuild. {why} The old image keeps serving. |
| Image state, before the first build | not built yet |
| Images list title and line | Images; Each machine has one Linux image. Its repositories' jobs run from it. |
| Image row status | Missing {layers} · needed by {repositories} (amber); Not built yet (quiet); Has what its repositories need |
| Image row note | {n} extra layer, {n} extra layers, base only |
| Image facts line | Built {time} · {size} · used by {repositories}; Not built yet. Choose layers now; it is built when its first runner starts. |
| Missing layers banner | {repository} needs {layer}; {repositories} need {layers}; Add it, Add them |
| Layers pane | Layers; Add what your repositories' jobs need. The basics are always included. |
| Layer purposes | for browser tests, for Windows builds from Linux, for MSI installers, for .deb and .rpm packages, for tests that boot a VM |
| Layer needed by | needed by {n} (amber when needed and not ticked) |
| Layers footer | Cancel, Save and rebuild (Save before the first build); Rebuilds after running jobs finish. The old image keeps working until then. |
| Folded part | What is inside this image; Always included: {tools}; Extra packages set by repositories: {repository}: {packages}; None; Rebuild |
| Layer line, read only | {layer} · on |
| Layers differ | The saved layers differ from the built image. Rebuild to use them. |

## Labels

- A button says what it does: "Save", not "OK".
- A headline says where the user is: "Reports", not "reports/list".
- A field name is a noun: "Title", not "Enter the title".
- Mark only optional fields, with "(optional)" after the name.

## Machine policy words

The words of the machine mode, the battery pause and the CPU cap (machines R5.61 to R5.64, tray R5.42, web-console R5.65). The tray and the console use the same words.

| Word | Where and meaning |
|---|---|
| When it works | The section title on the machine page. |
| Mode | The name of the one-choice list and of the tray submenu. |
| Automatic | A mode. Takes jobs. Pauses on battery and while you have paused it from the tray. |
| Always | A mode. Takes jobs on battery too. A pause from the tray still holds. |
| Paused | A mode, and a state word. Takes no new job. The jobs that run finish. |
| Takes jobs | The column of the machines list. |
| Finishing {n} jobs (up to {m} min) | State word of a machine that stopped new jobs and still runs jobs ({n} running jobs it waits for, {m} the time to finish running jobs; one job: "Finishing 1 job"). The list and the filter say "Finishing jobs"; the Overview says "finishing jobs". |
| Not taking jobs | State word of a machine that stopped new jobs and has no running job. The filter says the same. |
| Time to finish running jobs | The setting field (machine and pool defaults), in minutes. Help: "How long running jobs may take to finish when new jobs are stopped. crsp never cuts a job." (pool: "...when a machine stops new jobs.") |
| Waiting for jobs to finish | Section title of the job that holds a machine that stopped new jobs. |
| Stop new jobs on {machine}? | Question card title. Lines: "Running jobs finish first, for up to {m} min." and "Nothing is removed. Resume undoes it." Primary: Stop new jobs. |
| Taking jobs | State word: the machine takes new jobs. |
| Paused, On battery | State words: why the machine takes no new job. |
| Busy | State word: the load is over the budget, so a new job waits. The tray header does not say it. |
| Needs a base image, Disk low | State words: a backend it may use has no runner base image on the machine; the disk is almost full. |
| You stopped new jobs from the console. The jobs that run finish. Resume takes jobs again. | The note under Now for Not taking jobs. |
| The mode is Paused. It takes no new job. The jobs that run finish. | The note under Now for Paused by the mode. |
| It was paused from the tray. It takes no new job. The jobs that run finish. | The note under Now for Paused from the tray. |
| New jobs are stopped. Resume takes jobs again. | The hint under Resume in the page header. |
| Now | The facts line with the state word. |
| Paused until | The end of a pause from the tray with a timer. A date and time (Dates, times and numbers). |
| Until you resume | The value of the fact Paused for a pause from the tray that has no end. |
| Power | The facts line. Values: On battery, On mains. Shown only when the machine's battery state is known. |
| Pause on battery | The switch. Saved at once. |
| Always ignores the battery. | The reason beside the Pause on battery switch in Always; the switch is off and disabled. |
| Off: the machine keeps taking jobs on battery. | The note under the switch when it is off. |
| On: Automatic takes no new job while the machine runs on battery. | The note under the switch when it is on. |
| CPU for runners | The name of the CPU cap list. Values: 25%, 50%, 75%, 100%. |
| The most CPU the runner processes may use. The load budget stays as it is. | The help under the CPU cap list. |
| Not supported on macOS yet. The runners use all the CPU. | The help under the CPU cap list on macOS. |
| Not supported on macOS yet. | The reason on each CPU cap value on macOS. |
| The CPU cap was not set. {why} | Problem message when the agent could not set the cap. |
| Setting the CPU cap. | Note while the agent has not yet reported the result for the chosen value. |
| It takes no new job. The jobs that run finish. | Note under the state when the machine is paused. |
| It takes no new job while it runs on battery. The jobs that run finish. | Note when the machine is on battery. |
| Its load is over the budget. A new job waits until the load drops. | Note when the state is Busy. |
| Could not save. {why} | Problem message when a change was not saved. |
| Could not change the mode. Could not pause. Could not resume. | Titles of the tray notice when a menu change failed. |
| This service cannot change the mode. Open the console and go to Machines. | Tray notice for a service that does not report a policy. |
| waits because its machine is paused, waits because its machine runs on battery | Job wait reasons on the jobs screen. |

## Tray menu words

The words of the tray menu (tray R5.1, R5.42; owner decision D10, TRAY3). No entry is bold.

| Word | Where and meaning |
|---|---|
| Open console | The tray entry that opens the console, signed in. |
| Open Doctor | The tray entry that opens the console Doctor page. |
| Open weekly digest | The tray entry that opens the newest weekly CI run insights digest, a draft file on this machine (feature ci-run-insights R5.38; CIX23). Shown only when a digest was written. |
| Pause | The tray submenu. Its items are "for 1 hour" and "until I resume". |
| Paused until {time} - Resume now | The tray entry in place of Pause while a timed pause holds. {time} is hours and minutes. Pressing it resumes. |
| Paused - Resume now | The same entry for a pause with no end. |
| Mode: {mode} | The tray submenu; {mode} is Automatic or Always. It holds those two modes only. |
| Running - {n} jobs - {m} runners | The tray status line. Pressing it opens the console Jobs page. Reads "Running - no runners yet" before the first runner. |
| ↑ Update to {version} | The tray entry for a newer release. It opens the update dialog, which asks first. |

## Connect GitHub words

The words the Connect GitHub step shows when GitHub sends the person back with an error (github-integration R5.3, web-console setup screens). The page picks the message by the error code in the address and then clears the code from the address.

| Word | Where |
|---|---|
| GitHub made the App, but crsp could not save it. Open the App on GitHub and delete it (App settings, Advanced, Delete GitHub App). Then start again. | Problem message when the App exists at GitHub and nothing was kept. |
| Open the App on GitHub | The button that opens the half-made App in a new tab. |
| GitHub App names are unique across GitHub. If GitHub says the name is taken, delete the old App or choose another name. | Help under the App name field. |
| crsp could not save the App because the OS secret store is not available. See the doctor. Then start again. | Problem message, code unavailable. |
| GitHub could not be reached. Nothing was changed. Try again. | Problem message, code github_unavailable. |
| The link back from GitHub did not work. It ran out or was used already. Start again. | Problem message, code bad_request. |
| An App is already set up. | Problem message, code conflict. |
| The App was not created. Start again. | Problem message, any other code. |

## Notification words

The events, severities and channel kinds of the notifications feature, as people read them (notifications R5.1, R5.4). The stable type names stay in the API and in webhook bodies; the screen shows these words.

| Word | Meaning |
|---|---|
| Machine offline | A machine's agent link has been down longer than the grace time. |
| Job waiting | A queued job has waited longer than the set time. |
| Disk low | Free space on a machine's work disk is below the threshold. |
| Runner outdated | A runner's version is older than the pinned version. |
| Doctor finding | The doctor reports a new finding. |
| Update available | A newer stable release exists. |
| Info, Warning, Error | The lowest severity a channel gets. |
| Email, Webhook, Desktop | The kinds of channel. |
| Channel | One way of being told: an email address, a webhook or a desktop notice. |

## Why a job waits

The words of the list "Why jobs wait" (scheduling R5.48). The gate's own line with its numbers goes after the colon; the reason alone gives the plain sentence.

| Reason | Words |
|---|---|
| Section title | Why jobs wait |
| Table caption | Waiting jobs |
| Column | Why it waits |
| Empty | No jobs are waiting. |
| No machine for the OS or labels | No machine has this OS and these labels. |
| Over the budget | Over the load budget: {numbers}. (Over the memory or CPU budget.) |
| No free slot | No free slot: {n} ahead in the queue. |
| Parallel limit | The repository is at its parallel limit. |
| Machine drained | The machine is not taking jobs. |
| Load not measured | Load not measured yet. The machine cannot read its load, so the job waits. Run the doctor to see why. |
| Reason not known | Waiting. The reason is not known yet. |

## Component words

The words of the shared components (`design_system.py components`). A project changes a word here and passes it to the component through its `labels` prop. `{min}`, `{max}`, `{n}`, `{unit}` and `{pattern}` are filled in by the component.

| Word | Component |
|---|---|
| Previous month | Date picker |
| Next month | Date picker |
| Month | Date picker |
| Year | Date picker |
| Or type the date | Date picker |
| Type the date as {pattern}. | Date picker |
| Pick a date from {min} to {max}. | Date picker |
| Hours | Time picker |
| Minutes | Time picker |
| Seconds | Time picker |
| More {unit} | Time picker |
| Fewer {unit} | Time picker |
| Morning or afternoon | Time picker |
| Quick picks | Time picker |
| {n} hours | Time picker |
| {n} minutes | Time picker |
| {n} seconds | Time picker |
| Delete last digit | Number keypad |
| Type a number. | Number keypad |
| The lowest is {min}. | Number keypad |
| The highest is {max}. | Number keypad |
| Type all {n} digits. | Number keypad |
| From {min} to {max}. | Number keypad |
| Drop the file here, or | File field |
| Choose a file | File field |
| Change | File field |
| Remove {name} | File field |
| Choose a {pattern} file. | File field |
| Full error | Table: the button of a failed runner's long reason |
| Details | Table: the button of any other long text (Doctor, Jobs) |

## Pool size words

The words of the Pool size section on a machine's page (machines R5.58, LOOK22). It is a plain section of the page's one form; there is no wizard and no Save of its own (UX-71).

| Where | Word |
|---|---|
| Section | Pool size |
| Fact names | Pool size now, Room for the pool, CPU budget |
| Not set | not set |
| Budget not known | Budget: not known yet; the three choices say The budget is not known yet. |
| Choice name | New pool size |
| Choices | Recommended: half of the free room ({memory}, {cpus}); Recommended: three quarters of the free room ({memory}, {cpus}); Custom |
| How computed (line under a recommended choice) | Half of the {room} free for the pool, rounded down to whole GiB. CPUs: the same share of the CPU budget ({budget}), at most one per 1.5 GiB. (Three quarters: the same with Three quarters.) |
| Custom | Choose the memory and the CPUs yourself. Fields: Pool memory (help: Memory the pool may use on this machine, in GiB.), Pool CPUs (help: CPUs the pool may use on this machine.) |
| Mistakes | The pool needs at least {n} GiB. The pool needs at least {n} CPUs. |
| Reading | Reading the budget of {machine}. |
| Check line, fits (done) | This size fits the budget of the machine. |
| Check line, little left (needs you) | This size fits, but leaves {left} for this PC. (Shown when less than 3 GiB of the room is left.) |
| Check line, lower size (needs you) | This lowers the pool size. Jobs that need more than the new size wait or fail to start. |
| The tick | Yes, lower the pool size |
| Check line, does not fit (problem) | This size does not fit the budget. {reason} The nearest sizes that fit: {sizes}. (memory over the room: see Too big) |
| Too big (memory over the room) | {requested} is too much: only {room} is free for the pool right now (other programs use {used}). Use {fit memory} and {fit cpus}? |
| Too big, button | Use {fit memory} and {fit cpus} (applies both sizes to the fields; it does not save) |
| Too big, CPU reason | {fit cpus}: each CPU needs about 1.5 GiB. (shown when the CPUs change with the memory) |
| Too big, room note | The room moves with what else runs on this PC. |
| Cannot check | The size cannot be checked yet. {why} Try again when the agent has reported. |
| Reasons beside the page's Save | Reading the budget. The size cannot be checked yet. This size does not fit. Change the pool size. Tick the box to confirm lowering the pool size. |
| Changed while editing | The pool size changed to {size} while you edited. Nothing was saved. |
| Not saved | The pool size was not saved. {why} Try again. |

## Extra packages words

The words of the extra packages form on a repository's page (runners R5.51).

| Where | Word |
|---|---|
| Section | Extra packages |
| Field | Extra packages (optional) |
| Help | Distribution packages added to the pool image, one name per line. |
| Note | A changed list builds a new image aside. Running jobs keep the old image until they end. |
| Mistake | Line {n}: {name} is not a package name. Use letters, digits and + - . only, and do not start with -. |
| Buttons | Save packages (busy word: Saving), Cancel (puts the saved list back) |
| Done | Extra packages saved. |
| Failed | Could not save the packages. {why} |

## Theme words

The words of Settings, Appearance (UX-60).

| Place | Word |
|---|---|
| Choice label | Theme |
| Choice help | The colours of the console. The choice is saved at once. |
| Theme names | Phosphor, Tokyo Night, Catppuccin, Gruvbox, Solarized, Nord |
| Mode label | Mode |
| Mode help | System follows your device. The choice is saved at once. |
| Mode choices | System, Dark, Light |
| Passing message | Saving the mode. |
| Failure | Could not save the mode. |
| Key-hint bar | `/` search, `j k` move, `Enter` open, `?` keys |

## Demand and webhook words

The words of the doctor findings `demand.quiet` and `github.webhook` (github-integration R5.37, doctor R5.16).

| Where | Word |
|---|---|
| Quiet, ok | The demand source sees the queued jobs of {repository} |
| Quiet, warn | {repository} has {n} jobs queued for the pool and the demand source has seen none |
| Quiet, first detection | Quiet since {time}. |
| Quiet, restarted | Quiet since {time}. The controller restarted the demand loop once, at {time}, and it is still quiet. |
| Quiet fix, first | The controller restarts the demand loop once by itself. Run the doctor again in a minute. |
| Quiet fix, restarted | Restart the controller service. If it stays quiet, check that GitHub is reachable and the App can read Actions (github.app.permissions), then read the controller log for "poll queued jobs". |
| Webhook, ok | Webhook deliveries arrive ({n} in 24 hours, {n} failed) |
| Webhook, idle | No webhook delivery in 24 hours, and no job is open |
| Webhook, failed | {n} of {n} recent webhook deliveries failed |
| Webhook, none | No webhook delivery arrived in 24 hours while jobs are open |
| Webhook fix | Check that GitHub can reach the controller (the webhook address in the App settings). The polling fallback is in use meanwhile. |

## Hosted fallback words

The words of the hosted fallback on a repository's page, the list and the Overview (github-integration R5.42 to R5.46).

| Where | Word |
|---|---|
| Section | Hosted fallback |
| Choice | When no crsp machine can take these jobs |
| Choices | Wait for a crsp machine, Run on GitHub-hosted runners |
| Help | Waiting costs nothing. Jobs stay queued on GitHub for up to 24 hours. |
| Reason, public | A public repository never runs on GitHub-hosted runners. |
| Fields | Monthly cap of hosted minutes (help: 0 means no cap. Once the cap is reached this month, jobs wait again.), Hosted label for {os} (optional; help: Blank uses {default}.) |
| Note | GitHub may bill these minutes for a private repository. crsp switches back as soon as a machine is up. |
| Button | Save fallback (busy word: Saving) |
| Question card | Title: Run on GitHub-hosted runners? Lines: GitHub may bill the minutes that run on GitHub-hosted runners for a private repository. / Jobs of {repository} run there only while no crsp machine can take them. crsp switches back as soon as a machine is up. Action: Turn on (busy word: Saving) |
| Done | Saved. |
| Column and fact | Jobs go to: crsp machines, GitHub-hosted runners, waiting for a machine, pool off |
| Why | A crsp machine is up. / No crsp machine for {os} is up, so jobs run on GitHub-hosted runners. / No crsp machine for {os} is up. Jobs wait for one. / No crsp machine for {os} is up. Jobs move to GitHub-hosted runners if none comes back soon. / The monthly cap of hosted minutes is reached. Jobs wait for a crsp machine. / A public repository never runs on GitHub-hosted runners. Jobs wait for a crsp machine. |
| Fact | Hosted minutes this month |
| Overview line | {n} repository runs on GitHub-hosted runners because no crsp machine is up. / {n} repositories run on GitHub-hosted runners because no crsp machine is up. |
| Doctor, waiting | Jobs of {repository} wait: no crsp machine is up for {os} |
| Doctor, on hosted | Jobs of {repository} run on GitHub-hosted runners ({os}) |

## Joining a machine, idle and busy, and webhook health words

The words of the Add a machine wizard (machines R5.3a, R5.4a), the runner states (web-console R5.23) and the webhook health of a repository (github-integration R5.50).

| Where | Word |
|---|---|
| Join choice | How the machine joins (help: Copy one command and run it on the machine, let crsp sign in over SSH once, or download a file to open on the machine.): Copy a command, I have SSH, Download an installer |
| Windows note | Copied command: Run the command in an administrator terminal. / Over SSH: A Windows machine needs the OpenSSH server, and the user must be an administrator. |
| SSH fields | Host, User, SSH port, SSH key file (help: Paste the content of the private key file. crsp uses it once for this join and never keeps it. There is no password option; the user needs sudo without a password. On Windows: the user must be an administrator.) |
| SSH errors | Enter the host name or address. / Enter the user name. / Paste the SSH key file. |
| Review facts | System, How it joins (A copied command / Over SSH as {user} on {host} / A downloaded installer), Join code (Lasts 15 minutes and works once) |
| Review lines | crsp signs in once with your key, runs the install command and signs out. The key is not stored. / Next you get one command with the join code in it. Run it on the machine. / Next you get an installer file for this system with the join code in it. Open it on the machine, then delete the file. |
| Join command | Run this command on the machine now. The code shows once. / crsp ran the install command over SSH. Waiting for the agent. |
| Installer saved | The installer is saved as {file name}. It holds a one-time join code, valid 15 minutes. Delete the file after you use it. Then, per system: Windows: On the machine, double-click it and accept the administrator prompt. Waiting for the agent. / macOS: On the machine, open it. If it does not open, run it with sh in Terminal. Waiting for the agent. / Linux: On the machine, run it with sh. It asks for sudo. Waiting for the agent. |
| Installer wait | Making the installer (the wait text while the file is made) |
| Installer failure | Could not make the installer. {reason} |
| Join steps | Waiting for the agent, Agent connected, Machine ready; each says done, in progress or not yet |
| SSH question card | Title: Is this the host key? Line: Host {host} shows the key {fingerprint}. Action: Yes, sign in (busy word: Signing in) |
| SSH failure | Could not join over SSH. {reason} |
| Runner state | idle, busy |
| Runner counts | Idle, Busy, Not ready |
| Runner job | {repository} {job}, running {duration} |
| Repository column and fact | Last delivery: no delivery yet, delivered, delivery failed, delivery missed, no webhook, pool off; Last webhook delivery; Jobs are learned by: webhooks, polling |
| Webhook notes | The GitHub App of this controller has no webhook, so GitHub tells crsp nothing. crsp finds jobs by polling GitHub every 15 seconds, and jobs still run. Nothing needs fixing unless you want jobs to start at once. / GitHub did not tell crsp about a job; crsp found it by polling, so it ran a little late. The webhook of the GitHub App cannot reach this controller. In the GitHub App settings, check that the webhook address is public and starts with https, and look at Recent Deliveries for the red mark. |
| Polling notice | GitHub deliveries to this repository failed {n} times in a row, so crsp now polls GitHub for its jobs. Jobs still run, but they start a little later. |
| Polling button | Switch back to webhooks (busy word: Switching) |
| Polling failure | Could not switch back. {reason} |

## Agent recovery words

The words of the Recovery section and the State word of a machine whose agent needs recovery (operations R5.44, web-console R5.33).

| Word | Where and meaning |
|---|---|
| needs recovery | State word in the State column and in the facts of a machine whose agent needs recovery. |
| Recovery | The section title on the machine page. |
| The agent on this machine is too old to work with the controller. | Problem message when the reason is an old protocol. |
| The agent on this machine was asked to update an hour ago and did not. | Problem message when the agent is stuck on an old version. |
| Agent has, Agent needs, Since | The facts: the version the agent runs, the version the controller runs, and since when (a past time, Dates, times and numbers). |
| Recover | The one action. Admin only (busy word: Recovering). Locked after it worked. |
| Recovery is ready. The agent gets the update on its next call. | Done message after the call, and for a machine that is already armed. |
| Could not start recovery. {why} | Problem message when the call is refused or fails. |

## Offline runner cleanup words

The words of the action that deletes the registration of an offline runner whose machine cannot remove it (github-integration R5.33, web-console 4.5).

| Word | Where and meaning |
|---|---|
| Delete registration | The button in the Managed column of a managed runner whose state is offline, and the action of its question card. Admin only (busy word: Deleting). |
| Delete {runner}? | The title of the question card. |
| The registration of offline runner {runner} on GitHub. | First line of the card: what goes. |
| Its record here, and its folder if its machine answers. | Second line of the card. |

## GitHub App key words

The words of Settings, GitHub App, where the owner replaces the App private key (github-integration R5.7).

| Word | Where and meaning |
|---|---|
| GitHub App | The tab. |
| GitHub App key | The section title. |
| New private key file | The file field. Help: The file you download from the App's page at GitHub. crsp checks it with GitHub first and keeps the old key if GitHub refuses it. |
| Replace key | The one action. Owner only (busy word: Replacing). |
| Choose the private key file from GitHub. | Mistake when no file, an empty file or a file over 16 KiB is chosen. |
| The new key is in use. | Done message after the controller stored the key. |
| Could not replace the key. {reason} | Problem message when the call is refused or fails. The reason never holds the key. |

## Pool per organization words

The words of the section on the repositories screen (github-integration R5.21).

| Where | Word |
|---|---|
| Section | Pool per organization |
| Lead | Organization runners work in one runner group that only the repositories with the pool on can use. Only an owner of the organization on GitHub can turn this on. Personal accounts keep repository runners. |
| Columns | Organization, Runners, Runner group, Repositories in the group, Use organization runners |
| Switch | Organization runners for {organization} (the switch saves at once; words On and Off) |
| Status | organization runners, repository runners |
| Reason, not available | The pool per organization is not available here. / the reason the controller names, for example that the App lacks the organization runner permission |
| Failure | Could not change the pool. {reason} |
| Refusal | you are not an owner of the {organization} organization in GitHub. |

## First machine words

The words of the setup step "Add the first machine" when a machine is already in the pool (machines R5.1, web-console 4.1).

| Word | Where and meaning |
|---|---|
| This PC, {host name}, is in the pool. | Done message when the controller's own machine is in the pool. |
| Add the first machine is done. | Done message when a machine is in the pool and it is not the controller's own. |
| Add another machine | The one action. It opens the Add a machine wizard for other computers. |

## Half-registered runner words

The Remove button of two Doctor findings: a GitHub runner no machine runs, and a runner record GitHub does not list (doctor R5.50, runners R5.94). Remove here takes only that one registration or record away, and the card says so.

| Word | Where and meaning |
|---|---|
| Remove | The one button of each finding and the action of its card. Admin only (busy word: Removing). |
| Remove {runner} from GitHub? | Title of the card for a GitHub runner no machine runs. |
| No machine runs this runner. Only this registration is removed from GitHub. | Intro of that card. |
| The registration of runner {runner} on GitHub | The line the card lists for it. |
| Remove the record of {runner}? | Title of the card for a record GitHub does not list. |
| GitHub does not list this runner, so it cannot take jobs. Its record is removed from crsp. | Intro of that card. |
| The record of runner {runner} in crsp, and its service and folder on the machine | The line the card lists for it. |
| {runner} on GitHub belongs to no machine | Title of the first finding. |
| {runner} is not registered on GitHub | Title of the second finding. |
| {runner} cannot write its shared tool folders | Title of the finding that a runner user cannot write cargo-target, cargo-home, rustup-home, xwin or the tool cache (doctor R5.51, caches R5.57). Its fix is a command shown as text. |
| {runner} can write its shared tool folders | The same finding when it is fine. |

## Runner state words

The state word of a runner on the Runners screen (web-console R5.23, runners R5.79; UX-19). A word other than idle or busy shows its reason in a muted line under it.

| Word | Meaning and reason line |
|---|---|
| idle | GitHub lists the runner online and not busy, and its service runs. No reason line. |
| busy | GitHub says the runner has a job. No reason line. |
| not registered | GitHub does not list a runner with this name. Reason: "GitHub does not list a runner with this name". |
| stopped | The service of the runner is stopped or missing. Reason: "its service is stopped" or "its service is not on the machine". |
| offline | GitHub lists the runner offline. Reason: "GitHub reports it offline". |
| unknown | GitHub could not be read. Reason: "GitHub could not be read, so the state is not known". |
| failed | The runner did not start. The reason line is the reason of the failure. |
| offline, with "Waiting for GitHub to release the old session, until {time}" | After a hard restart GitHub still holds the runner's old session for a few minutes. A wait, never a failure (runners R5.95). The reason line ends with the time the wait is over. |
| offline, with "GitHub still holds the old session of this runner after the wait window; look for a second listener or another machine that uses this runner name" | The wait is over and GitHub still refuses the session (runners R5.95). |
| Remove | The action on the row of a failed runner (busy word: Removing). Asks first in the question card, which names the runner (UX-9, UX-62). |


## Linux on a Windows machine words

The words of Add runners and the machine list for a Windows machine whose container engine runs Linux containers (runners R5.47, machines R5.18).

| Word | Where and meaning |
|---|---|
| Windows, runs Linux containers | The System column of the machine list for a Windows machine with Docker Desktop on WSL2. |
| Linux runners on this Windows machine run in containers, through Docker. The container backend must be allowed on the machine. | Note in Add runners when the chosen machine runs Windows and the system is Linux. |
| The container backend is not allowed on this machine. Linux runners here need it. | Problem on Add runners step 1 when the system is Linux, the machine runs Windows and the container backend is off. It is also the reason Next is disabled. |
| Allow it on the machine | The button beside that message; it opens the machine page. |
| Nothing would be added with these choices. Change them and try again. | Problem on Add runners step 1 when the plan has no step, so Review is not reached. |
| {reason}. | A refusal of the plan, shown on Add runners step 1 with a full stop, so the user changes a choice. Try again after a failure goes back to step 1 with the reason in the same place. |
| This machine runs windows, not linux. | The reason a machine is greyed for a system it cannot run (also for a Windows machine without Linux containers). |
| WSL is installed but not ready; run wsl --install | Backend reason, shown only when WSL2 does not work and Docker does not run on it. It is written once. |


## List toolbar words

The words of the shared list toolbar (components 11 and 12, CRSP132).

| Word | Where and meaning |
|---|---|
| All | The first choice of a segmented filter. It shows every item. |
| {n} selected | In the toolbar, after Select all N and Clear, when something is selected. |
| {n} repository, {n} repositories | The count in the Repositories toolbar. The same pattern for machine, runner, image, cache and user. |
| Search images, Search caches, Search users | The labels of the search fields of Images, Caches and Users and roles. |
| Pool on, Pool off | The two choices of the Pool filter (with All). |

## Public repositories words

The words of the setting that lets the pool serve public repositories, and of the screens that show it (web-console R5.68 to R5.70, CRSP131).

| Word | Where and meaning |
|---|---|
| Enable for public repositories | The switch in Settings, Repositories. Off by default. |
| Off: the pool serves private repositories only. Keeping repositories private is safer. | Line under the switch while it is off. |
| On: the pool can serve public repositories you turn it on for. Jobs from forks stay refused. | Line under the switch while it is on. |
| Are you sure? | Title of the question card for both directions. |
| Turning this on lets the pool serve public repositories. | Intro of the question card when turning on. |
| Anyone can open a pull request on a public repository. / A self-hosted runner runs that code on your machines. / Jobs from forks stay refused. / Secrets, caches and files on the machine could be reached. / Keeping your repositories private is safer. | The five lines of the question card when turning on. Primary: Turn on. Busy word: Turning on. |
| Turning this off stops the pool for public repositories. | Intro of the question card when turning off. |
| {n} public repositories use the pool now. Their runners are drained and removed, and their pool is switched off. The settings are kept. | The line of the question card when turning off ({n} is public_on_pool; the singular uses repository, uses, Its and its). Primary: Turn off. Busy word: Turning off. |
| No public repository uses the pool now. Nothing is stopped. | The line of the question card when turning off and none is on the pool. |
| The pool was switched off for {n} public repositories. Their settings are kept. | Done message after turning off. |
| Types: Private (default) / Types: Private & Public | The line near the title of Repositories. The value links to the setting. |
| Visibility, Private, Public, Internal | The column and its values. |
| Public repositories are off, so the pool does not serve this one. | The reason in a greyed public row. Link: Turn on public repositories. |
| Use pool | Primary action of the action bar on Repositories, for the ticked repositories. Also the action on a repository's page. |
| Stop using pool | Primary action of the action bar on Repositories when every ticked repository already uses the pool. Also the action on a repository's page. |
| Nothing selected | Status line of the action bar on Repositories while nothing is ticked. |
| {n} selected | Status line of the action bar while {n} rows are ticked. |
| Tick the repositories that should use the pool. | Reason of the disabled Use pool while nothing is ticked. |
| Discard changes? | Title of the question when the person leaves an edit screen with unsaved changes. Body: You changed this page and did not save. If you leave now, the changes are lost. Answers: Keep editing, Discard. |
| Changed | The mark on a field whose value differs from what is saved. |
| Unsaved changes ({n}) | Status line of the action bar on an edit screen while {n} things differ from what is saved. |
| Nothing has changed yet. | Reason of the disabled Save in the action bar while nothing differs from what is saved. |
| Change a setting to save. | Reason of the disabled Save on a machine's page while nothing differs from what is saved. |
| Public repositories are off. Turn them on in Settings first. | The reason of a disabled Use pool, and of a repository that Add runners cannot choose. |
| Public repositories are off: {names}. Turn them on in Settings first. | The reason of the disabled Turn on N repositories when the selection holds a public one. |
| {n} public repositories are not listed: public repositories are off. Turn them on in Settings to use the pool there. | Note in Turn on the pool. Action: Open the setting. |
| Fix it | The link in the list from a repository with a Problem to its page. |
| Turn on public repositories, Turn off the pool | The two actions of the Problem of a public repository that uses the pool while the setting is off. |

## First run in the console words

The words of the first-run fixes (web-console 4.1 and 4.3, CRSP124). The console does everything; no step sends the user to a terminal or to GitHub's settings.

| Word | Where and meaning |
|---|---|
| First run is not finished. Next: {step}. | The banner. {step} is connect GitHub, add the first machine or turn on the first repository. The action is Open the setup page. |
| Add a repository | The button that opens GitHub's install page for the App in a new tab (Repositories, Turn on the pool). Busy word: Opening GitHub. |
| Install the App on an organization | The same button in Add runners, when the App is on no organization. |
| GitHub is open in a new tab. Pick the account and the repositories there, then come back to this tab. The list refreshes by itself. | Note under the install button after it opened GitHub. |
| GitHub saved the App installation. The list below is read again. Tick the repositories you want and press Use pool. | Done message on Repositories after GitHub's return with setup_action install or update. Action: Use pool. |
| GitHub sent the install request to an owner of the account. The repositories show here when they approve it. | Needs-you message after the return with setup_action request. |
| No repository is waiting. Install the App on a repository to turn the pool on for it, or open Repositories to see the ones that already use it. | Needs-you message in Turn on the pool when no repository is free. |
| {n} runners failed. The State column says why. Remove a failed runner with Remove, then add runners again. | Needs-you message in Runners. |
| Clear search and filter, Clear search and filters | The action of the "hidden by the search" note in Machines and Runners. |
| Tick {backend} | The action that ticks an installed backend that is not turned on (machine page). |
| {backend} is installed and works on this machine, but is not turned on. Tick it and press Save so this machine can run its runners. | Needs-you message on the machine page. |
| {backend} is installed on this machine but not turned on. Open the machine, tick it and press Save to let it run runners. | Needs-you message on Done of Add a machine. Action: Open the machine. |
| Remove this machine | The section of the Remove button, with: "Removing takes the machine and its runners out of the pool. Jobs that run there finish first. The agent stays installed on the machine." |
| Open Machines | The one primary on Done of Add a machine when it is not part of the first-run checklist. |
| Runners are added to one repository. An organization appears here when the App is installed on one. | Note in Add runners when the App is on no organization. |

## Access for CI words

The words of the Settings section Access for CI and of the action that sets up parity for a repository (web-console R5.90 and R5.91, github-integration R5.82 to R5.84, CRSP134).

| Word | Where and meaning |
|---|---|
| Access for CI | The Settings section, and the title of its table. |
| Only an admin can make, see or revoke access for CI. Ask an admin to do it. | Needs-you message for a person who is not an admin. |
| An access for CI is a value a CI job uses to read the pool. It can read the runners and run the doctor, which is all that the parity check needs. It cannot change a machine, a runner, a setting or a user. | Note above the table. |
| No access yet. Set up parity for a repository below, or make one here. | Empty table. |
| Name, Can do, Made by, Made, Last used, State, Actions | The columns. |
| Read only: the checks of parity | The Can do cell. |
| never, live, revoked, Revoked | Last used and State values, and the Actions cell of a revoked access. |
| Revoke | The danger button of a row and of its question card. Busy: Revoking. |
| Revoke {name}? / Access {name} stops working at once. A CI job that uses it is refused. | Title and line of the question card. |
| Make an access | The section of the form. |
| Write a name for the access, such as "parity CI". | Error when the name is empty. |
| What it can do / Parity | The field and its one choice; description: Read the runners and run the doctor, nothing else. |
| Make | The primary button of the form. Busy: Making. |
| Could not make the access. {reason} | Problem message when making fails. |
| Access {name} is made / I have it | Title and close button of the card that shows the value once. |
| This is the only time the value is shown. crsp keeps no copy of it. Copy it now, or revoke this access and make another. | Needs-you message on that card. |
| Access value | Label of the code block that holds the value. |
| Set up parity | The section of the action. |
| No repository yet. Install the App on a repository first (Repositories). | Empty state of the section. |
| crsp writes the access as the secret CRSP_ACCESS, and its address and certificate fingerprint as the variables CRSP_URL and CRSP_CA_FINGERPRINT, to the repository through the GitHub App. You copy nothing. The parity workflow reads them. | Note of the section. |
| Repository | The field that chooses the repository. |
| Set up parity | The primary button (one line at phone width). Busy: Setting up. Reason when none is chosen: Choose a repository first. |
| Could not set up parity. Nothing was left behind. {reason} | Problem message when the setup fails. |
| Parity is set up for {repository}. The workflow can run now. | Done message. |
| The GitHub grant | See GitHub grant words: a missing App permission is shown by the shared part, not by words of this screen. |

## Docker disk words

The words of the Docker disk section on the machine page (caches R5.51 to R5.54, machines R5.66). The section shows only for a machine whose agent found a Docker Desktop disk file, or whose choice is On or Off.

| Word | Where and meaning |
|---|---|
| Docker disk | The section title on the machine page. |
| Make Docker's disk smaller | The name of the one-choice list. Values: Automatic, On, Off. Saved at once. |
| On for a Windows machine with a Docker Desktop disk file. Off for the others. | Help under Automatic. |
| Makes the disk smaller when the numbers below say so. | Help under On. |
| Never makes the disk smaller by itself. | Help under Off. |
| Start when this much can be reclaimed | The name of the number field, in GiB. The default is 30. |
| Wait until the machine has been idle for | The name of the number field, in minutes. The default is 30. |
| No job runs or waits for its runners. | Help under the idle field. |
| It starts at any hour, once the machine has been idle for that long and that much can be reclaimed, and at most once a day. New jobs wait in the queue while the disk is made smaller. No job is stopped. | The note at the end of the section. It says when the compact runs. |
| Now | The facts line with the state word. |
| Disk file, Docker uses, Can be reclaimed, Last time | The other facts lines. Sizes as in Dates, times and numbers. Last time is a date, a time and the sizes, or the failure. |
| Making the disk smaller | State word while the compact runs. |
| Off | State word: the policy is off for this machine. |
| Done today | State word: it ran in the last 24 hours. |
| Waiting: the machine is offline | State word. |
| Waiting: the machine is paused or not taking jobs | State word. |
| Waiting for the agent to look at the disk | State word before the first report. |
| Waiting: less than the limit can be reclaimed | State word. |
| Waiting: jobs run or wait here | State word. |
| Waiting: not idle long enough | State word. |
| Waiting: another change is running | State word. |
| Cannot: the agent has no administrator rights | State word, shown as a failure. |
| Not looked at yet | State word when no look was made. |
| The agent on this machine does not run as administrator, so it cannot make Docker's disk smaller. Install the agent again from an administrator window, or turn this off. | Problem message under the facts. |
| Turn off | The action beside that message. Saves Off. |
| Could not save. {why} | Problem message when a change was not saved. |
| Make the disk smaller now | The button that starts the compact by hand (caches R5.58). Busy label: Making the disk smaller. Disabled with the reason when the agent has no administrator rights or the compact is running. |
| Make Docker's disk smaller on {machine}? | Title of its question card. |
| The machine is drained first. The disk is made smaller when the running jobs have ended. | Intro of the card. |
| Can be reclaimed: {size} | First line of the card: the file size minus what Docker uses. |
| New jobs wait in the queue; no job is stopped | Second line of the card. |
| Docker restarts, and the machine takes jobs again when it is done | Third line of the card. |
| Make smaller | The confirm button of the card. Busy label: Starting. |
| {machine} is making Docker's disk smaller ({size} on disk, {size} used). New jobs wait in the queue and start again when it is done; no job is stopped. | Notice to the projects, before. |
| {machine} made Docker's disk smaller: {size} to {size}. Jobs take the machine again. | Notice to the projects, after. |
| {machine} could not make Docker's disk smaller: {why}. Jobs take the machine again. | Notice to the projects, on a failure. |

## GitHub grant words

The words of the shared part that guides a missing App permission or repository access (github-integration R5.86, web-console R5.93, CRSP139). Permission words: Secrets (write), Variables (write), Administration (write), Actions (read), Contents (read), Metadata (read), Organization runners (write).

| Word | Where and meaning |
|---|---|
| The GitHub App needs {permissions}. Nothing is lost: do the steps below, one at a time. | Needs-you message above the step list. |
| Install the App on the account | Label of the install step. Text: Pick the account and the repositories on GitHub. Done: The App is installed. |
| Ask for {permissions} on the App page | Label of the ask step. Text: On the App page, under Repository permissions, set the access to Read and write. Done: The App asks for it. |
| Accept the new permissions on the installation: {permissions} | Label of the accept step. Text: On the installation page, press Accept new permissions. Done: The installation accepted it. |
| done, missing | The state word of a step. |
| Do this after the step above. | Text of a missing step that is not next. |
| Install the App on GitHub, Ask for it on GitHub, Accept it on GitHub | The one primary action; it opens the page of the next missing step in a new tab. |
| GitHub is open in a new tab. Do the step there, then come back to this tab. It checks again by itself. | Note after the action is pressed. |
| Checking GitHub. | Note while it reads GitHub. |
| Could not check GitHub. {reason} | Problem when the check fails; the next check tries again. |
| Ready. {permissions} granted. | Done message when everything is granted. |

## Update dialog words

The dialog the tray opens when a newer stable release is found (tray R5.43, operations R5.87, CRSP147). It stays until answered. The buttons are Later and Install now, and the link is What changed (action vocabulary).

| Word | Where and meaning |
|---|---|
| Updated to {version} | Tray notice, once per version, after a successful update. |
| CI Runners Shared Pool {version} is the newest stable release. | Tray notice when Look for updates finds nothing newer. |
| Install now waits for the {n} running jobs to finish. It stops none. | Line of the dialog when jobs run; names how many. |

## GitHub error words

The words of the repositories page when the list is the last copy because the read from GitHub failed (github-integration R5.89 and R5.89, web-console, CRSP148). Say what GitHub really said (UX-28).

| Word | Where and meaning |
|---|---|
| Your GitHub sign-in ended. Sign in with GitHub again to refresh the list. | Needs-you message; the sign-in is memory-only and ended, GitHub itself is fine. |
| GitHub could not be reached. | Needs-you message when no answer came. |
| GitHub refused ({status}): {message}. Grant the permission in the guide below. | Needs-you message when a permission is missing. |
| GitHub answered {status}: {message}. | Needs-you message for any other GitHub error, with GitHub's own status and words. |
| The list could not be refreshed. | Needs-you message when no reason is known. |
| The list is the last copy, from {when}. | Second sentence of each message above; without a time: The list is the last copy. |

## Network words

The words of Settings > Console > Network and of the dialog that chooses the controller address (operations R5.90, OPS14). Also opened from the Needs-you box of Access for CI.

| Word | Where and meaning |
|---|---|
| Network | Section title. |
| Choose an address | Primary action in the section and in the Needs-you box; opens the dialog, and is its title. |
| Use this address | Primary action of the dialog; busy word Saving, then Restarting. |
| Listening on | Fact: the address the controller listens on now. |
| Chosen, after the next restart | Fact: the stored choice is not yet the running address. |
| This PC's network address | Choice (recommended), with its address on line 2; description: Runners and machines on your network, and containers on this PC. |
| Docker and WSL on this PC only | Choice: containers and WSL on this PC, no other machine. |
| Another address | Choice: opens the box Address as host:port. |
| Address as host:port | Box label; help: an address of this PC, port 8460 unless written. |
| The controller restarts once when you press Use this address. Running jobs finish first. | Line in the dialog. |
| Waiting for running jobs to finish, then the controller restarts on {address}. No job is stopped. This page reopens here when the controller answers. | Note while it waits. |
| The controller answers on {address}. It also keeps 127.0.0.1 for this console and the tray. | Done. |
| This address is set by CRSP_LISTEN_ADDR on this PC, so it cannot be changed here. | Note when the environment decides. |
| Could not use this address. {reason} | Problem when the address is refused. |
| The controller did not listen on the new address. {reason} | Problem when it restarted but could not bind. |
| Add this sign-in address to your GitHub App | Title of the guided step, in the dialog before the switch (github-integration R5.8, OPS16). |
| GitHub only signs people in on addresses the GitHub App knows. It does not know this one yet. Add this callback URL to the App: copy it, open the App's settings on GitHub, and under Callback URL choose Add Callback URL. | Text of the guided step, with the box Callback URL (Copy) and the action Open the App on GitHub. |
| Callback URL | Label of the copyable box. |
| Open the App on GitHub | Link button to the App's settings page, github.com only. |
| When you have added it, press Try again. The controller then tries a sign-in for {address} and switches to it. Until then it stays on {address}. | Line in the guided step; Try again is the primary action, busy word Asking GitHub. |
| GitHub does not know this address yet. Pressing Try again before you added it changes nothing. | Note in the guided step. |
| Could not check the address. {reason} | Problem when GitHub could not be asked. |
| Sign-in does not work on {address} yet. Open the console on the PC that runs it at http://127.0.0.1:8460, or add this address to the GitHub App. | Needs-you message on the sign-in page; the guided step follows and the action is Try again. |
| {login} is not on the list. Ask the owner to add you. | Problem message on the sign-in page, code not_allowed (github-integration R5.11, OPS17). |
| The sign-in took too long or was already used. Try again. | Problem message on the sign-in page, code state_missing (github-integration R5.8, OPS17). |
| The sign-in started on another address. Try again from this address. | Problem message on the sign-in page, code state_mismatch (github-integration R5.8, OPS17). |
| Sign-in does not work on this address. Use https, or http://127.0.0.1 on the PC that runs the controller. | Problem message on the sign-in page, code address_refused (github-integration R5.8, OPS17). |
| GitHub did not accept the sign-in. Try again. | Problem message on the sign-in page, code exchange_failed (github-integration R5.8, OPS17). |
| GitHub did not say who you are. Try again. | Problem message on the sign-in page, code user_failed (github-integration R5.8, OPS17). |
| The controller could not start your session. Try again. | Problem message on the sign-in page, code session_failed (github-integration R5.8, OPS17). |
| The sign-in could not return to the address you started on. Try again. | Problem message on the sign-in page, code handoff_failed (github-integration R5.8, OPS17). |
| This controller has no GitHub App yet. Open the setup page to create it. | Problem message on the sign-in page, code no_app (github-integration R5.8, OPS17). |
| The OS secret store is not available. See the doctor. | Problem message on the sign-in page, code secret_store_unavailable (github-integration R5.8, OPS17). |
| Sign-in failed. Try again. | Problem message on the sign-in page, code sign_in_failed (github-integration R5.8, OPS17). |

## Repositories list: groups and inactive types

The words of the Repositories screen when the list is grouped by owner and hides repositories of an inactive type (web-console, LOOK5; owner decisions LOOK D15 and D16). Active types today: Private.

| Word | Where and meaning |
|---|---|
| {owner} | Group heading row, with a tick box that selects the whole group. |
| {n} repositories · {m} use the pool | Under the owner; `1 repository` when n is 1. |
| {n} public repositories are not listed: the pool serves private repositories only. | Note line above the list while public repositories are not an active type. |
| List them too | Link in the note: shows them, in grey, in their groups. |
| Change active types | Link in the note: opens the active-types setting. |
| {n} public repositories are listed in grey: the pool serves private repositories only. | The note line while they are listed. |
| inactive type | Tag after Public on a listed public repository that is not an active type (UX-70). |
| Hide them | Link in that line: lists private repositories only again. |
| {n} private repositories | Count beside the toolbar; `{n} private & public repositories` when public is active or listed. |

## Chart words

The fixed words of the waterfall, job graph and charts (components 50 to 55, RES15). Every other word comes in through props.

| Word | Where |
|---|---|
| Show as table | The folded section under every chart that holds its text alternative. |
| now | Waterfall: the line and its legend ("now: 4 min"). |
| critical path | Waterfall and job graph: said in words for a job on the critical path. |
| Name, Detail | Waterfall table heads. |
| Job, Stage, after, Detail | Job graph: table heads; "Stage {n}" heads a stage; "after: a, b" under a job. |
| Series, When, Value, Range, Note | Trend chart table heads. |
| Day, Total, Busiest hour | Heatmap table heads. |
| Mon, Tue, Wed, Thu, Fri, Sat, Sun | Heatmap day labels on the grid (the narrow column); the table under it keeps the full weekday. |
| forecast, actual, forecast = actual | Forecast chart axes and the diagonal's legend; table heads. |
| inside the forecast range, outside the forecast range, no range | Forecast chart: said for each job. |
| lowest, highest, last, values | Chart and sparkline text alternatives ("5 values, last 4 s"). |
| no data yet | A sparkline, heatmap or forecast chart with nothing to draw. |

## Ordered list words

| Word | Where |
| --- | --- |
| Move {item} up | Name of the icon button ↑ on a row. |
| Move {item} down | Name of the icon button ↓ on a row. |
| {item} moved to place {n} of {total}. | Live line after a move. |

## Account menu words

The words of the account menu at the top right and of the Users and roles intro (web-console R5.95, LOOK15).

| Word | Where and meaning |
|---|---|
| GitHub sign-in: active until {HH:MM} | Menu line while the GitHub sign-in holds; the time is local. |
| GitHub sign-in: active | The same line when no end time is known. |
| GitHub sign-in: ended | The same line, amber status, after a restart or the 8 hours. |
| Sign in with GitHub again | Action in the menu when ended: signs out, then asks GitHub, then returns to this page. |
| Opening GitHub | Busy word of that action. |
| GitHub sign-in ended | Accessible label of the amber dot on the account button. |
| Who may use this console. Your own GitHub sign-in is in the {login} menu at the top right. | Intro line of Users and roles. |

## Update page words

The words of the small update page the tray opens (tray R5.43, web-console R5.96). The page is the shared wizard (component 4); the steps follow UX-30.

| Words | Used for |
|---|---|
| Updates | The Settings tab after Console (LOOK43). It holds the Updates pane; Console no longer does. |
| Update | Title of the page and name of the wizard. |
| Version {version} is ready. Running jobs finish first; the service restarts (about 1 min). | The Review line. It is also the text of the native fallback box. |
| Update now | The primary on Review. Busy word: Updating. |
| Not now | The left button on Review: nothing starts. |
| Running jobs are safe. | Line under the progress bar while the update works. |
| Hide | Closes the window while the update goes on. It stops nothing. |
| Downloading, Waiting for jobs, Installing, Restarting | The work steps, in the order the service goes through them. |
| Downloading {n}%, Waiting for running jobs to end, Installing {n}%, Waiting for another installer, Restarting the service | The live line of the current step. |
| The service is restarting. This page waits for it. | Note while the service is down for the install. |
| Updated to {version}. | The Done message. |
| Close | The primary on Done, and the left button after a failure. |
| Open Updates | The left button on Done: opens Settings, Updates. |
| The update did not finish. {reason} The running version stays. The log is {path}. Open the log folder from the tray menu. Then try again. | The failure message. |

## Native update window words

The words of the native update window on Windows (tray R5.46 to R5.49). The steps and buttons follow UX-30 and UX-71; the web page of the same update is in Update page words.

| Words | Used for |
|---|---|
| Update - CI Runners Shared Pool | The title of the window. |
| Update to {version} | The heading on Review. |
| Version {version} is ready. You run {version}. | First line on Review. |
| Running jobs finish first, then the service restarts (about 1 min). No job is stopped. | Second line on Review. |
| Not now, Update now | The two buttons on Review. Update now is the main one and the default. |
| Starting the update. | The line between pressing Update now and the first answer. |
| Downloading {version} | The heading while it downloads; the bar shows {n}%. |
| The update is checked against its checksum and signature before it installs. | The line under the download bar. |
| Waiting for jobs | The heading while the machine drains. |
| {n} jobs run now. The update starts when they are done. | The job line; one job reads `1 job runs now. The update starts when it is done.` and none reads `The jobs on this machine are finishing. The update starts when they are done.` |
| This machine takes no new jobs meanwhile. No job is stopped. | The note while it waits. |
| Hide | Closes the window; the update goes on. The default while it works. |
| Cancel update | Stops the update while it downloads or waits for jobs. Busy word: Cancelling... |
| Installing {version} | The heading while it installs; the bar shows {n}% when the service knows it. |
| This cannot be cancelled. You can hide this window; the update goes on. | The note while it installs. |
| Waiting for another installer | The heading while another program holds Windows Installer (TRAY11). Indeterminate bar; the button is Hide. |
| {name} is installing. Windows installs one program at a time; this update starts when it finishes. | The note under it. {name} is the other installer; without a name it reads `Another program`. |
| Restarting the service | The heading while the service is replaced. It shows only once the installer has finished. |
| The service is replaced and starts again. This window waits for it. | The note while the service is down after the installer finished. |
| Updated | The heading on Done. |
| Now running {version}. | The Done line, and the body of the toast after an automatic update. |
| Close | The button on Done, Failed and Cancelled. |
| The update failed | The heading on Failed. |
| Still on {version}. | The line after the reason on Failed and after a cancel. |
| Log: {path} | The last line on Failed. |
| Update cancelled | The heading after Cancel update. |
| Up to date | The heading when nothing newer exists: the service says it is already the newest, or the version offered is not newer than the one that runs. Also the title of the tray notice. |
| Now running {version}. This is the newest stable release. | The line under Up to date; the button is Close. |
| Nothing changed. Still on {version}. | The line after a cancel. |
| CI RUNNERS SHARED POOL | The name in the band and in the installer bitmaps (capitals: the art is drawn in a 5 by 7 pixel font). |

## Shared Pool and Jobs words

The words of the seven-item menu, Shared Pool and the Jobs sub-sections (owner decisions D24, D29, D31 to D34, LOOK31). "Runners" stays "runners" (D29).

| Word | Where and meaning |
|---|---|
| Shared Pool | Menu area and page title: the one pool of machines and their runners. |
| Summary, Machines, Runners, Images and caches, Size and backends | The tabs of Shared Pool. |
| All, By repository, By machine, By runner type, By job type, Wait and duration | The tabs of Jobs. |
| One pool across your machines. Set it up here, then choose the repositories that use it. | Lead of the Shared Pool summary. |
| Set up the pool | Title of the first-run guide. |
| Three steps. A step opens when the one before it is done. | Hint of the first-run guide. |
| 1 Add a machine / 2 Set runners / 3 Choose repositories | The steps of the guide; each shows `done, {n} ...` when finished. |
| Add a machine first. | Reason on a step or button that cannot start yet. |
| {n} machines, {m} runners, {k} busy | The pool totals above the runner list and on the summary. |
| Runners serve every repository that uses the pool. | The one line on Add runners, in place of the Repository question. |
| How a job gets run | The first-run pane of the Overview: the picture of Help (component 64), replacing the old *How crsp works* strip (LOOK44). |
| Open Help | Button of that pane that opens Help. |
| Hide this | Button that hides the pane for good in this browser. |
| Showing the last 100 finished jobs. | Note above the finished jobs when more exist. |
| See all | Button that pages in the earlier finished jobs. |
| Done, failed and the averages count the last 200 finished jobs. | Quiet line under a grouped Jobs table. |
| not placed yet | Group name of a job that has no machine or runner yet. |
## Runner service words

The words of a runner service that did not start (runners R5.89, web-console R5.98, tray R5.44, doctor R5.45). The agent has already given the service account the right to log on as a service again and tried twice.

| Where | Word |
|---|---|
| Banner (problem) | {runner} on {machine} did not start. {reason} |
| Banner and Doctor action | Start. It asks that machine's agent to give the account the right again and start the service. Busy word: Starting. Disabled for a user who is not an admin, with the reason. |
| Tray notification title | A runner did not start |
| Tray notification text | {runner} did not start. {reason} Open the console and press Start. |
| Doctor finding, the right is missing | The account {account} of {runner} cannot log on as a service |
| Doctor finding, the service is stopped | The service {service} of {runner} is not running |
| Doctor finding, fine | {account} can log on as a service and {runner} runs |
| Doctor fix | Press Start. The agent gives the account the right to log on as a service again and starts the service on {machine}. |
## Webhook settings words

The words of Settings > Access > Webhooks and the link on the repository page (github-integration R5.51, web-console R5.99, OPS25).

| Word | Where and meaning |
|---|---|
| Webhooks | Section title; the name of the state line. |
| on / off / failing | Status words of the state: on, off, failing (the newest delivery failed or was missed). |
| Real webhooks | Check box. Help: "Clear it and Save to find jobs by polling GitHub again." |
| Public https address | Field name; help: "The address that forwards to this controller, for example https://hooks.example.com. crsp sends a signed test to it before GitHub is changed." |
| With webhooks GitHub tells crsp about a job the moment it is queued, so the job starts at once. Without them crsp asks GitHub for new jobs every 15 seconds, so a job can start up to 15 seconds late. Webhooks need an address GitHub can reach: a tunnel or a reverse proxy with a real certificate. A LAN address or a self-signed certificate does not work. | The explanation at the top of the section. |
| Testing the address | Busy word of Save while the address is tested; Turning off is the busy word when the box was cleared. |
| Webhooks are on. GitHub now tells crsp about jobs at once. | Done line after Save. |
| Webhooks are off. crsp finds jobs by polling GitHub again. | Done line after turning off. |
| Could not save the webhook. {reason} | Problem line; the reason is the controller's own plain words. |
| The address must start with https://. GitHub does not send webhooks to plain http. | Mistake in the list. |
| {host} is a local address. GitHub can only reach a public address, such as a tunnel or a reverse proxy with a real certificate. | Mistake in the list. |
| Write the public address that forwards to this controller. | Mistake when the field is empty. |
| This is not a web address. Write it like https://hooks.example.com. | Mistake. |
| Write the address without a user name, a query or a #fragment. | Mistake. |
| The App does not listen for workflow jobs yet. On GitHub, open the App's permissions page and under Subscribe to events tick Workflow job, then save. Jobs are learned by webhooks once you have done that. | Guided step; its button is Open the App on GitHub. |
| Turn on webhooks | Link at the end of the repository note "no webhook"; goes to Settings > Access > Webhooks. |


## Runner kind words

The words of Add runners step 1, the Runners list, the runner page and the Repositories page for the kind of a runner (runners R5.89 to R5.92, github-integration R5.60, web-console R5.101, R5.102; owner decisions D93, D94, D95, 2026-11-18).

| Word | Where and meaning |
|---|---|
| Kind | The label of the one-choice list on Add runners step 1. |
| Persistent | Choice. Line under it: Keeps its tools and caches between jobs. Starts jobs faster. |
| Ephemeral | Choice. Line under it: A fresh container for every job. Nothing carries over. Recommended for public repositories. |
| Ephemeral is chosen because a public repository uses the pool. | The one line under Kind while no one has picked and a public repository has the pool on. |
| Persistent is chosen because no public repository uses the pool. | The same line when no public repository has the pool on. |
| {Kind} is chosen because {Other kind} is not offered here. | The same line when the preferred kind cannot run on the machine for the system. |
| Ephemeral needs the container backend, which is not allowed on this machine. | Reason of the greyed Ephemeral choice. "which does not work on this machine" when the backend is allowed but not usable. |
| Persistent Linux runners on this Windows machine need the container backend, which is not allowed on this machine. | Reason of the greyed Persistent choice for Linux on a Windows machine. |
| Ephemeral runners are not available for Windows yet. Windows runners are persistent. | Reason of the greyed Ephemeral choice for Windows. The same for macOS with its name. |
| The container backend decides which kinds this machine offers. | Note with the button Allow it on the machine, shown while a kind is greyed for the container backend. |
| Keep downloaded dependencies | Check box on Add runners step 1 for a persistent runner (on by default) and on the runner page. |
| Temporary files of each job are always removed. | Help line of that check box. Not a setting. |
| Most ephemeral runners at once | Label of the count for an ephemeral runner. Help: The pool makes a fresh container for each job, up to this many at the same time. |
| Allow {n} ephemeral runner(s) | The primary of Review for ephemeral runners (busy word: Registering). |
| {n} persistent runner(s) on {machine}. | Review text, then the dependency sentence: Downloaded dependencies are kept between jobs. / Downloaded dependencies are removed after each job too. / Each job gets a fresh container (ephemeral: Up to {n} ephemeral runner(s) at once on {machine}.). |
| Persistent. Keeps dependencies: yes | Third line of the Isolation cell in the Runners list. Ephemeral shows Ephemeral only. |
| Dependencies | Section of the runner page for a persistent runner. |
| Dependency cache size | Fact of the runner page. Not measured yet when the controller has no size. |
| Clear now | Button of the runner page (busy word: Clearing). |
| A job is running. Clearing waits for it to finish. | Line under Clear now while the runner is busy. |
| not clean | State word of a runner whose clean-up failed. |
| Not clean: {reason}. It takes no new job until it is clean. | The runner's problem on its page and the full text of the State cell. |
| Runner kind | Column of the Repositories list, fact and field of a repository. Choices: Use the default ({Ephemeral only or Any runner}), Ephemeral only, Any runner. |
| Ephemeral only (default) | Value while the owner has not chosen; the default is Ephemeral only for a public repository, Any runner for a private one. |
| Jobs from a public repository can then run on a persistent runner, which keeps files between jobs. Anyone who can open a pull request may reach it. | Warning under the field when a public repository is set to Any runner, and the body line of the question card Let public jobs use persistent runners? (answer Allow). |
| No ephemeral runner capacity: jobs would wait. | Line in the Runner kind column and a Problem on the repository page with the button Add runners, while the pool has no machine that can make an ephemeral runner. |

## Jobs section words

The words of the new Jobs section (web-console 4.6, proposed). A wait names what is happening (UX-29); a status is a word, a glyph and a colour (UX-58); a chart has a text equivalent (UX-41, UX-17).

| Word | Where and meaning |
|---|---|
| Queue, Running, Done | Tabs and pane titles of the three lists. |
| One row per run, One row per job | The count slot of the Jobs list toolbar: says what a row is in the chosen view. |
| By repository, By machine, By runner, By runner type, By job type, Charts | The sub-section tabs. |
| Order | Column: the job's place in the queue, 1 is next. |
| Workflow and job, Branch, Runner and machine, Waited, Ran, Forecast, Starts, Doing now, Progress | Column heads. |
| Why it waits | Column: the wait reason in words (UX-29), a long one behind Details. |
| not placed yet | A queued job has no runner. |
| (worktree {name}) | After the branch, only when the job reported a worktree or task. |
| not reported | Fact value when the job did not report a worktree or task. |
| Show 25 more | Button of the Done pane; adds 25 jobs up to 100. |
| Only the last 100 done jobs are kept. Older ones are not shown. | Note under the Done list at the limit. |
| Showing {n} of {m} kept done jobs. | Note under the Done list. |
| Showing only jobs of {filter}. / Show every job | Note and button when a group row opened a filtered list. |
| about 6 to 9 min, about 40 to 55 s | A forecast or ETA range. |
| not enough history yet | No forecast: fewer than 5 runs. No number is made up. |
| median of the last {n} runs on {machine} | The basis of a forecast; also "of the jobs of this workflow" and "of this repository". |
| changed lately, so the last 10 runs count | Added to the basis when the median moved by more than a quarter. |
| in about 3 to 4 min / now | When a queued job should start; "(a forecast, not a promise)" on the job page. |
| Open the run on GitHub | The one primary of the job and run pages. |
| Open the commit, Open the pull request | Secondary buttons on the job page. |
| Open the whole run | Button to the run page. |
| Project and code, Run and attempts, Steps, Runner, Times, Forecast, History of this job | Pane titles of the job page. |
| Job graph, Timeline, Jobs | Pane titles of the run page. |
| Job, Stage, Needs, Status, Runner, Waited, Ran, Forecast, Critical path | Column heads of the Jobs table of the run page: every job once, one line a row. Needs says "nothing" for a job that needs none; Critical path says "critical path" or "no"; a job with no runner says "not placed yet"; a time not known says "not yet". |
| Every job of the run once, with the critical path marked. | Hint of the Jobs pane. |
| The table under Jobs says the same in words. | Last sentence of the hints of the Job graph and the Timeline: the text alternative of both (UX-41). |
| This run has 1 job; the job has steps. | Lead of the run page for a run of one job; "This run has {n} jobs; each job has steps." for more. |
| persistent, state kept between jobs / ephemeral, fresh for every job | Runner type (UX-61). |
| warm, the caches were ready / cold, the caches were rebuilt | Cache state of a job. |
| Run time and wait per day, Jobs per day, Forecast against actual, Busiest hours | Chart pane titles. |
| {n}% of {m} finished jobs ran inside their forecast range. The aim is about 80%. | Line over Forecast against actual. |
| Median wait, Median run, Slow end of run, Jobs failed, Weight, Light, Heavy, Backend, Runner type | Column heads of the grouped tables and the Done runs list; the slow end is the run time 9 in 10 jobs stay under. |
| Steps are no longer kept for a job this old. / This job reported no steps. | Steps pane of the Job page: older than 30 days, or GitHub sent none. |
| This repository is not on the list of repositories, so no run belongs to it. | A list filtered by a repository the controller does not know. |

### Runs first (RES16, owner decisions of 18 Nov 2026)

| Word | Where and meaning |
|---|---|
| Runs, Jobs | Switch above Queue, Running and Done: one row for each workflow run, or the flat job list for filtering by runner, machine and kind. |
| Place, Starts | Queue run row: the smallest place of its queued jobs, and the start range ("in about 4 to 7 min"). |
| starts in about 4 to 7 min, 3rd in line | Start range and place of a queued job; the reason it waits (UX-29) is beside it. |
| Jobs done, Jobs queued | Column: "7 of 9". |
| Total time, Time so far, Ends | Columns of a done run, of a running run, and the end forecast of a running run (one number: "in about 7 min"). |
| Slowest job, Running now | Columns: the job that took longest, the job that runs now. |
| Commit, Event | Columns of a run: the 7-character commit ("a1b2c3d"), and what started it ("push", "PR #52", "schedule"). The Run page keeps one line, Commit and event: the commit and the event word ("a1b2c3d, push"), never the pull request number, which is the fact Pull request. The Worktree fact is left out when it equals the branch. |
| Result | Column of a run: success, failure, timed out, cancelled, running or waiting. |
| (task RES15) | After the branch, when the branch starts with a task id. A worktree shows as "(worktree {name})" only when the job reported it, otherwise "not reported". |
| Task, Worktree | Facts of the Job and Run pages. "none in the branch name" and "not reported" are their empty words. |
| Last 7 days, Last 30 days, Last 90 days | Range switch of Done. |
| Show 25 more | Adds 25 runs or jobs up to 100 a page; older ones by range and filter. |
| Export CSV, Export daily summaries (CSV) | Buttons of Done and of Charts. |
| crsp keeps steps for 30 days, jobs for 90 days and daily summaries for 400 days. | Footer of Done, Charts and the group tables. |
| 7 days, 14 days, 30 days | Range switch of Charts; 14 is the first. |
| Backend, Weight | Columns of By runner type (docker, wsl2, hyperv) and By job type (Light, Standard, Heavy badge). |
| about 7 min | One number in a table. A range ("about 6 to 9 min") with its basis ("median of the last 20 runs") is for the Job and Run pages. |
| not enough history yet (workflow typical about 4 min) | A job with fewer than 5 runs: no number is made up; the workflow's or the repository's typical time is named and labelled. |
| Step times are from GitHub. | Hint of the Steps pane. |
| expands at run time | Marks a matrix job or a reusable workflow in the job graph. |
| Arrows come from the workflow file at commit {sha} (read once per commit and kept). | Line over the job graph. When the file cannot be read: "no arrows are drawn: jobs that ran at the same time are shown side by side. This is a guess from timing, not from the workflow." |

### One-line row words (CRSP210, UX-72)

The short forms a one-line table shows; the whole line is the hover text and sits under the row's button (*Why*, *Details*). A workflow is its file name (`ci.yml`), the run number is the *Run* column (`#44723830522`, `, attempt 2`).

| Wait reason | Short form |
| --- | --- |
| free slot | waits for a slot |
| load over budget | machine load too high |
| no runner with labels | no runner with its labels |
| repository parallel limit | repository limit |
| machine drained | machine not taking jobs |
| load not measured | load not measured yet |
| paused | machine paused |
| on battery | machine on battery |
| unknown | reason not known yet |

### Row action words (RES17, UX-72)

One word set for the button at the end of a table row. The guard reads this table: a label not listed here fails the `rowActionWords` guard of `e2e/tests/web-console/ux-routes.spec.ts` and the Jobs rows spec `e2e/tests/web-console/ux-jobs-rows.spec.ts`.

| Word | Where and meaning |
|---|---|
| Open run | Row action of a run row (Queue, Running, Done): opens the Run page. |
| Open | Row action of a group row (By repository, machine, runner, runner type, job type). The group's name is already in the row. |
| Open job | Row action of a job row (Jobs view of Queue, Running, Done): opens the Job page. |
| Remove | Row action of a failed runner (Runners): removes it and does not open a page. A press, not a link. Admin only (busy word: Removing). |
| Edit | Row action of a user (Settings, Access): opens the change of its role. A press, not a link. Admin only. |
| Replace | Button of an unmanaged runner (Runners): replaces it with one the pool manages, after a question. A press. Admin only (busy word: Replacing). |
| Delete registration | Button of an offline managed runner (Runners): deletes its registration on GitHub, after a question. A press. Admin only (busy word: Deleting). |
| Revoke | Row action of a live access for CI (Settings, Access): stops it working, after a question. A press. Admin only. |
| Copy | The button of a code block in a cell (Doctor, a finding's command): copies the text and says Copied. It changes nothing. A press. |
| On, Off | The word of a switch in a cell (Repositories, the pool for a repository): it shows the state; pressing it changes the state. Admin only. |
| Why, Details | Button inside a cell that has a long text, to read it whole. Not a row action. |

## Jobs redesign words (LOOK40, concept A, owner decisions of 20 Nov 2026; feature ci-run-insights)

The words of the Jobs Overview, its new tabs and parts (UX-74 to UX-77, components 58 to 63). A word of a UX rule is not repeated here.

| Word | Where and meaning |
|---|---|
| Overview, Live, Done, Run elsewhere, Run insights, Charts | The six tabs of Jobs, in this order (D9 to D13, D20, D7, D8). Overview is first and the landing. *Live* replaces the tabs Queue and Running. |
| Group by | Choice above the Done list, with None, Repository, Machine, Runner, Runner type, Job type. It replaces the five *By* tabs (D11). |
| All, Queue, Running, Done | The one state filter of the list of Live, Done and Run elsewhere (LOOK41, owner decision of 20 Nov 2026; it replaces the chips *Queued, Running, No runner*). |
| Waiting now | KPI tile: how many jobs wait, with the oldest wait as its second line. |
| Wait slow end (p90) | KPI tile: the wait that 9 in 10 jobs stay under. Hint: *9 in 10 jobs wait less than this.* |
| Jobs that passed | KPI tile: the share of finished jobs that passed, with the flaky count. |
| Pool busy | KPI tile: the share of runner time spent running jobs. |
| vs the previous 7 days | The change line of a tile; *vs the previous 14 days*, *vs the previous 30 days* follow the range. 7 days is the default range. |
| better, worse, same | The word of a change, beside its glyph and colour (UX-58). A fall in *Waiting now* is *better*. |
| Needs you | Title of the strip of the top 3 findings by minutes saved a week. |
| Nothing needs you now. | Empty strip. |
| about {n} min a week | The minutes a finding saves. |
| sure, likely, a guess | The confidence words of a finding (D16, UX-76): *sure* means the data proves it, *likely* means the data points to it, *a guess* is an estimate. |
| How we know | Fold of a finding: the data behind it. |
| Show the jobs behind it | Link of a finding: opens the filtered list. |
| Dismiss, Restore | Buttons of a finding in Run insights only (UX-52). Busy words: Dismissing, Restoring. |
| To do, Done, Dismissed | The three lists of Run insights. |
| Where the time goes | Card and chart: waiting and running time per day (stacked). |
| Wait parts | Chart: the parts of waiting (GitHub, queue, slot hold and start, cleanup). |
| Busiest repositories | Card: a bar list of repositories by wait and run minutes. |
| Failed and flaky | Card: failed jobs and jobs that passed on a rerun of the same commit. |
| Runner timeline | Card and part title: lanes per runner on a time axis. On a machine page the title is *Runner timeline of {machine}*. |
| idle while queued | Legend and table word of a stretch where a runner stood idle while a job queued. |
| no jobs | A lane with no job in the range. |
| No runner has worked in this range. | Empty runner timeline. |
| Nothing to rank in this range. | Empty bar list. |
| Open the {n} jobs of {name} | Link of a bar list row; *Open the {n} jobs of that day* for a chart column. |
| Open the jobs that wait now | Link of the Waiting now tile. |
| Loading | Value area of a tile that is loading. |
| Run elsewhere | Tab: jobs GitHub gave to a runner that is not crsp; each row names that runner and its state follows GitHub (D7). |

## Jobs list words (LOOK41, owner decision of 20 Nov 2026; feature ci-run-insights)

The words of the one search list of Live, Done and Run elsewhere (component 67) and of the Overview built from it.

| Word | Where and meaning |
|---|---|
| State | Name of the state filter (All, Queue, Running, Done). |
| Search jobs, Search runs, Search run elsewhere | Names of the search box. Placeholder: *Search, or repo: state: runner: branch: job:*. |
| repo:, state:, runner:, branch:, job: | Search words that narrow one field; a plain word matches any text of a row. |
| Show details, Hide details | Name and tooltip of the details button (an icon button); *Hide details* while the panel is open. |
| Details: {row}, Close details | Name of the details panel and its close button. |
| Nothing matches. Clear the search or choose All. | Empty list after a search or a state choice. |
| Waits for or runs | Column of the All list: the wait reason of a waiting run, the job a running run does. |
| When | Column of the All list: when a run starts, ends or ended. |
| Starts, Ends, Finished | Fact names of a run in the details panel: by state. |
| Runner type | The word for persistent against ephemeral runners (owner rule); never *kind* in text a user sees. |
| Open run, Open job | The button of the details panel that opens the page; the row action keeps its words (UX-72). |
| Wait slow end (p90) | Overview tile (see Jobs redesign words). |
| Failed jobs | Overview card: how many of the finished jobs of the last 7 days failed; it opens the finished jobs. |
| Each number is compared with the 7 days before. | Line under the Overview cards. |
| Showing {n} of {m} finished {runs or jobs} in the last {days} days. | Note under the list when it holds finished rows. |

## Runner leftover words

The words of the Doctor finding that lists runner services, folders, accounts and downloads no controller owns, and removes them (doctor R5.48, web-console 4.8, OPS33). Here Remove takes the leftover off the machine for good, and the card says so.

| Word | Where and meaning |
|---|---|
| Remove | The one button of the finding, and the action of its question card. Admin only (busy word: Removing). |
| Remove these leftovers from {machine}? | The title of the question card. |
| Each item below is removed from this machine. This cannot be undone. | The line above the items of the card: what happens. |
| Service, Folder, Account, Download | The kind word that starts each leftover. |
| {kind} {name} ({size}) | One leftover in the card: its kind, its name and its size when it has one. |
| {kind} {name} ({size}), account {account}. {why} | One leftover in the finding. |
| Removed, Already gone, Refused, Failed | The result word of each item after the call. A reason follows Refused and Failed. |
| {kind} {name}: {result}. {reason} | One line of the result list in the finding. |
| Runner leftovers on {machine}: {kind} {name} removed, ... | Done message when every item went and the finding is gone. |
| The agent has no administrator rights. Run it as a service with rights. | The reason beside a disabled Remove when the finding says the agent needs rights. |

## Registry words (RES20, owner decision D18 of 19 Nov 2026; REG5)

The words of the Registry tab under Images and caches: the registry of ready-made job images that is part of crsp and not public. A row action there is `Open` (Row action words).

| Where | Word |
|---|---|
| Tab | Registry |
| Sections | Disk, Recommendations, Images in the registry, Where it came from, Layers shared with other images, Pulls by day, Machines that hold it, What is in it, Delete, Registry budgets, Budgets, Cleanup rules |
| Cards | Standard images, Custom images, Last 7 days, Where it runs |
| Facts | Pulls, Builds avoided, Time saved, Machine, Folder, Free on the disk, Next cleanup, Shared layers, Only this image holds, Last pulled, Software, Known vulnerabilities, Signed by crsp |
| Columns | Image, Key, Kind, Arch, Repos, Size, Pulls, Last pulled, State, Frees, Decided by |
| Kinds | Standard, Custom |
| States | in use, current, newer one found, superseded, unused {n} days, variant of {branch} |
| Decided by | your yes, crsp on {date} |
| Buttons | Edit budgets, Review the newer image, Review the unused image, Review the budget, Review the replacement, Replace the image, Delete |
| Replace wizard | Replace an image; steps Compare, Review, Replacing, Done; Keep the old image for {n} days; Open the registry |
| Budget fields | Standard images, Custom images, Suggest deleting an image unused for {n} days, Keep a replaced image for {n} days |
| Delete question | Delete {profile} {key}?; {size} freed; the layers shared with other images stay |
| Refusal | A runner uses this image. The registry refuses to delete it until that job ends. |
| Add runners | Pulling the job image from the registry on {machine}, {done} of {total} GB, about {time} left; The registry has key {key}, signed by crsp |
| Note words | crsp never deletes what you may still want without your yes. Layers two images share are stored once. |
| Banners | The registry is not running. {reason}; The registry is cleaning up. Machines can pull; new images wait until it ends. |
| Empty | Nothing to recommend. The registry is within its budgets.; No images yet. The first machine that builds a job image pushes it here. |
| Image page | Repositories that use it, Shared layers, Time saved this week, Layer, Shared, only here, {n} other images, no copy, used now, holds a copy, Not scanned yet. The result shows here when the scan ends., Go back to this one, Going back, Open the image, Open the registry, Open the variant, Open the cleanup |
| States (image page only) | staged, refused; The registry has not checked this image yet. It is offered to machines when it passes.; The registry refused this image. It stays in staging until the next cleanup. |
| Recommendations | {image} is a newer version of an image the registry holds; {image} was replaced and its days to go back are over; {image} was made for a branch that is gone; {image} is unused; The {kind} images are over their budget; Layers no image uses; {image} belongs to a profile that was removed |
| Budgets page | Together the two may be at most {size}, half the free disk.; Mirror of upstream images; Open the mirror |
| Mirror page | Mirror of upstream images; Mirror upstream images; Upstreams; Budget; Check a tag again after {n} minutes; Separate registries with commas. |
| Replace wizard (live) | Go back to the old image; Pointers already moved stay moved.; The base image changed too, so the new image is built on a newer base. |
| Add runners, Review | The registry has key {key}, {size}. A machine without a job image pulls it instead of building. |
| Add runners, pulling | Waiting for the controller to send the job image to {machine}. The pull goes on when it answers.; The job image could not be pulled, so no runner was added. Nothing was changed on {machine}.; Build it here instead |

## Registry job-log words (REG13)

The lines the agent writes to the job log while it asks the controller for a job image, waits, pulls, builds and pushes (`internal/backend/container/ensure.go`, `images.go`). People read them in the job log, so they are user words. `{n}` is a number, `{reason}` is the cause in plain words. `internal/backend/container/words_test.go` builds each line from the code and fails when a line and its row differ, or a row has no line. A change of a word here changes the Go string in the same branch.

| Where | Line |
|---|---|
| Waiting for the controller, under a minute | Waiting for the controller to say whether the image is in the registry (waited {n} s) |
| Waiting for the controller, a minute or more | Waiting for the controller to say whether the image is in the registry (waited {n} min {n} s) |
| The registry is busy, under a minute | The registry is busy; asking again in {n} s |
| The registry is busy, a minute or more | The registry is busy; asking again in {n} min {n} s |
| Pull starts, size not known | Pulling the job image from the registry |
| Pull goes on | Pulling the job image from the registry, {n} of {n} GB |
| Pull goes on, little time left | Pulling the job image from the registry, {n} of {n} GB, less than a minute left |
| Pull goes on, more time left | Pulling the job image from the registry, {n} of {n} GB, about {n} min left |
| Pull did not finish | The pull did not finish; trying again |
| Pull failed | The pull from the registry failed ({reason}); building the image here instead |
| Push failed | The image stays on this machine; it was not pushed: {reason} |
| Push done | Pushed the image to the registry staging |

## Run insights words (CIX22, owner decisions D7 and D8 of 20 Nov 2026)

Run insights is a tab of Jobs, placed before Charts. Estimates always say *estimate* or *about*. `{n}` is a number, `{x}` a value, `{time}` a time.

| Where | Words |
|---|---|
| Jobs tab | Run insights |
| Segments | Do next, Waits, Timeline, Tests, Guardrails |
| Filter chip, Queue and Running | Run elsewhere |
| Run elsewhere, empty | No job runs on another runner. |
| Run elsewhere, runner column | Runner and labels; "{runner} (group {group}; labels {labels})" |
| Do next, table | What to do, Project, Area, Saves ("about {n} min a week"), Since; row action Open |
| Do next, project of a pool finding | the pool |
| Do next, empty | Nothing to do. crsp found no waste in the last 7 days. |
| Do next, not enough data | Not enough finished jobs yet to rank anything. crsp needs a few days of CI. |
| Do next, digest fold | Weekly digest, {week} |
| Finding, panes | The fix, Evidence, Estimate |
| Finding, primary | Review the change |
| Finding, normal button | Open the workflow on GitHub |
| Overview line | {n} things to do next, about {m} min a week |
| Waits, panes | Wait, hold and run per day, By repository, By job |
| Waits, columns | Wait, middle; Wait, slow end; Hold, middle; Hold, slow end; Run, middle; Run, slow end; Holds over the cap |
| Timeline, notes | idle while {n} job(s) waited; after-merge work |
| Timeline, empty | No runner took a job in this range. |
| Tests, segments | Slowest, Flaky, Duplicated, No assertions |
| Tests, over the limit | {x} s, over the {kind} limit of {n} s |
| Tests, empty | No test reports found. crsp reads JUnit, Playwright, vitest, go test, nextest and TRX reports your CI uploads. |
| Guardrails, states | ✓ pass, ✕ break {n}, – off ({reason}), ◷ unknown |
| Guardrails, source | guard report of {date}, crsp check |
| Waiting on GitHub | Waiting for GitHub's hourly limit until {time}; {n} jobs not read yet. |
| Action wizard, steps | Review, Changing, Done |
| Action wizard, buttons | Make the change, Not now |
| Job page, fact | Runs on: "crsp runner {runner} on {machine}", "Another runner: {name} (group {group}; labels {labels})", "GitHub-hosted runner ({label})", "not known yet" |
| Job page, steps source | Steps from GitHub events. / Steps read from GitHub. |
| Job page, checked | Last checked with GitHub {ago}. |
| Job page, pane | CPU and memory; CPU, peak; CPU, slow end (p95); Memory, peak; Memory, slow end (p95) |
| Run page, pane | Savings |
| Run page, notes | This run was superseded; This commit ran twice; This code was tested again; This run went on after its first failure |
| Size and backends, section | Demand; columns Labels, Slots, Busy, Jobs an hour, Wait, middle, Wait, slow end |
| Size and backends, estimate | {labels}: with {n} more slot(s) the p95 wait would be about {x} (now {y}). An estimate from the last 7 days. |
| Size and backends, queue grows | The queue grows: at least {n} more slots are needed. |
| Repository page, panes | CI checks, Guardrails, Job slots |
| Repository page, CI checks | ✓ pass, ✕ fix, – skipped; columns Status, Rule, Job, Line, Fix; "Checked {time}." |
| Repository page, Job slots | No advice: the limit of this repository did not hold jobs back while slots were free. |

## Run insights follow-up words (CIX23, owner decisions D17 to D21 of 20 Nov 2026)

| Where | Words |
|---|---|
| Finding, secondary action | Hide for 7 days. Busy word: Hiding. After it, disabled with the reason "This finding is hidden for now." |
| Finding, after hiding | Hidden until {time}. It comes back then if it is still true. |
| Finding, hiding failed | Could not hide this finding. {reason} |
| Settings, Data, section | CI run insights |
| Settings, Data, period field | Update period. Help: How often crsp looks at your CI again. A slower period uses less CPU, memory and disk. |
| Settings, Data, period choices | After every CI run, After every few CI runs, Once an hour |
| Settings, Data, number field | Runs between updates ({n} runs, 2 to 50), shown only for After every few CI runs |
| Settings, Data, saved | CI run insights saved. |
| Settings, Data, refused | Runs between updates must be from 2 to 50. |
| Do next, cost warning, title | Analysing CI runs costs more than it should: {what is over, such as CPU 6.2% (limit 5%), memory 320 MB (limit 300 MB), disk 1.10 GB (limit 1 GB)} |
| Do next, cost warning, fix | Choose a slower update period in Settings: analyse after every 5th CI run, or once an hour. (After every few CI runs: once an hour. Already once an hour: Lower the days or the size of the CI data kept in Settings, or ask for a bigger machine.) |

## Help words

The words of the Help page and of the ? beside a term (LOOK44, owner pick I of 20 Nov, D57). English only. *Slot* is a shown word. *Runner type* is the word for persistent or ephemeral; Linux or Windows is the system, never the type.

| Word | Where and meaning |
|---|---|
| Help | The seventh area of the menu and the page title. |
| One picture, then the questions people ask. Each card names its box. | Lead of Help (LOOK48; replaces *How crsp runs your jobs, and what its words mean.*). |
| How a job gets run | Pane of the picture on Help and on the first-run Overview. |
| On GitHub / In your pool | The two groups of the picture. |
| 1 to 3 happen on GitHub. 4 to 7 happen in your pool. | Quiet line under the picture (LOOK48; replaces *7 parts. Press a part to jump to its question.*). |
| 2 waiting / none waiting / {n} of {m} busy | Examples in the Queue and Slot boxes. The other boxes show a repository, `#{run}, {event}`, `{job} + labels`, a runner name and a machine name, or their short meaning when nothing is known. |
| Repository, Run, Job, Queue, Slot, Runner, Machine | The seven parts. Their lines: a GitHub project; one start of a workflow; asks for a runner; waits for a slot; room for one job; does the work; your computer. |
| Questions, in the order a job meets them | Pane with every question card at once, ordered by box (no steps, no Back, Next or Start again since LOOK47; title since LOOK48). |
| Box {n}: {Name} | Badge on every card, with the numbered circle; a link to that box in the picture (LOOK48). |
| {n} of {m} slots busy | Text of the slots picture on the slot card. |
| Memory {n}% of {b}% / CPU {n}% of {b}% | Text of the load budget picture; {b} is the machine's own budget. |
| first one takes the free slot / Runner type. Linux or Windows is the OS. / the tray icon, bottom right of the PC | Words inside the card pictures. |
| What does shared mean? / What are a run and a job? / What are labels? / What does Run elsewhere mean? | Cards about GitHub, first in the grid. |
| Why is my job waiting? / What is a slot? / What is the load budget? / What is the queue? / What is a runner type? | Cards about the pool, second in the grid. |
| What is the Doctor? / What is the tray app? / What are images and caches? | Cards about looking after the pool, last in the grid. |
| Slot | Room for one job at a time on a machine. |
| Pool size makes slots: you set memory and CPUs on Pool size, and crsp turns that into slots, the number of jobs that run at once. | The sentence of the slot card and of the ? on Slots. |
| Load budget | The most memory and CPU a machine may use for jobs. Over it, new jobs wait. The card shows each machine's own budget (`memory 70%, CPU 55%`), read from the API. |
| {n} of {m} slots in use | The slots of each machine on the slot card. |
| Runner type | Persistent keeps its tools and caches. Ephemeral is fresh for every job. |
| Run elsewhere | Another runner runs the job, not your pool, for example a GitHub-hosted runner. |
| No machine yet. Add a machine to see its slots. / ... its load budget. | The card when the pool has no machine. |
| What does {term} mean? | Name of the ? button. {term} is Slots, Load budget, Queue, Runner type or Run elsewhere. |
| More about this in Help | The link in the ? answer. |
| Open Pool size / Open Jobs / Open the Doctor / Open Images and caches | Links on the cards. |
| Load budget | Fact row on the machine page: `memory {n}%, CPU {m}%`. |

## Run follow-up words (CIX24, owner decision D27 of 21 Nov 2026)

A run can start other runs when it ends (the Installers workflow after CI). The run is not finished while they are. `{workflow}` is the workflow's name, `{state}` the word of the status (*waiting*, *running*, *success*, *failure*, *cancelled*).

| Where | Words |
|---|---|
| Run page and run details, fact | Started next: "{workflow}, {state}"; several are joined with "; " |
| Run page, status line, and the Result column of the Done and All lists | follow-up running (a status, glyph and colour of *running*, beside the run's own result) |
| Run details, fact while a follow-up runs | Still running: follow-up running |
| Done and All lists, hover text of the Result | Started next: "{workflow}, {state}" |
| Timeline, row | Waited for a runner: {project} {job} |
| Timeline, note | follow-up run |
