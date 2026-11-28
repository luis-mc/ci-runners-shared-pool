#!/bin/sh
# Supervisor of the persistent Linux pool container (runners R5.93). It runs one
# runner for every file in $STATE/runners.d, each in its own session, and starts
# a dead runner again with back-off (2, 4, 8 ... 60 seconds; the count starts
# over after the runner stayed up for five minutes). A runner whose file is gone
# or has a `.stopped` marker is ended. A runner whose listener log says GitHub
# deleted its registration ("Registration was not found", "the runner
# registration has been deleted") can never connect again, so it is not started
# again: a `.lost` marker holds the reason, list.sh reports it to the controller
# and add-runner.sh registers the runner again and removes the marker (R5.152).
# The runners register with
# `config.sh --unattended --replace` (never --ephemeral) when they are added by
# add-runner.sh, so a restart only runs them.
STATE=${CRSP_STATE_DIR:-/state}
DEPS=${CRSP_DEPS_DIR:-/deps}
RUN=${CRSP_SUPERVISOR_RUN:-/tmp/crsp-supervisor}
TICK=${CRSP_SUPERVISOR_TICK:-5}
BIN=$STATE/bin
mkdir -p "$RUN" "$STATE/busy"

now() { date +%s; }

# self_sum is the checksum of this script's file as it is now on disk.
self_sum() { cksum <"$0" 2>/dev/null; }
# idle is true when no runner of this container has a job.
idle() {
  for b in "$STATE"/busy/*; do
    [ -e "$b" ] && return 1
  done
  return 0
}
SELF_SUM=$(self_sum)
echo "$SELF_SUM" >"$RUN/supervisor.sum"

# runner_env prints the environment of one runner as `env` arguments.
start_runner() {
  n=$1
  dir=$STATE/$n
  [ -x "$dir/run.sh" ] || return 1
  mkdir -p "$DEPS/$n" "/tmp/crsp-run-$n" "$dir/_work/_temp"
  # the container user has no passwd entry and a read-only root, so HOME would be
  # "/": every tool that writes under $HOME or XDG fails. Each runner gets its own.
  mkdir -p "$DEPS/$n/home" "$DEPS/$n/cache" "$DEPS/$n/config" "$DEPS/$n/share" "$DEPS/$n/corepack"
  (
    cd "$dir" || exit 1
    export CRSP_RUNNER_DIR=$dir CRSP_STATE_DIR=$STATE CRSP_DEPS=$DEPS/$n
    export HOME=$DEPS/$n/home XDG_CACHE_HOME=$DEPS/$n/cache XDG_CONFIG_HOME=$DEPS/$n/config
    export XDG_DATA_HOME=$DEPS/$n/share COREPACK_HOME=$DEPS/$n/corepack
    export TMPDIR=/tmp/crsp-run-$n
    export RUNNER_TOOL_CACHE=$DEPS/$n/tools AGENT_TOOLSDIRECTORY=$DEPS/$n/tools CI_TOOLS_DIR=$DEPS/$n/tools
    export npm_config_cache=$DEPS/$n/npm npm_config_store_dir=$DEPS/$n/pnpm-store
    export GOMODCACHE=$DEPS/$n/go/mod GOCACHE=$DEPS/$n/go/build CARGO_HOME=$DEPS/$n/tools/cargo
    export PIP_CACHE_DIR=$DEPS/$n/pip PLAYWRIGHT_BROWSERS_PATH=$DEPS/$n/playwright
    export ACTIONS_RUNNER_HOOK_JOB_STARTED=$BIN/job-started.sh ACTIONS_RUNNER_HOOK_JOB_COMPLETED=$BIN/job-completed.sh
    [ -f "$STATE/runners.d/$n.env" ] && . "$STATE/runners.d/$n.env"
    exec setsid ./run.sh
  ) >>"$dir/runner.log" 2>&1 &
  echo $! >"$RUN/$n.pid"
  now >"$RUN/$n.since"
}

# registration_lost succeeds when the newest listener log of runner $1 says
# GitHub deleted the registration, and the log is newer than the `.runner` file
# (config.sh writes it when the runner registers, so a log from before a new
# registration never counts). list.sh reads the same way.
registration_lost() {
  d=$STATE/$1
  f=$(ls -1 "$d/_diag"/Runner_*.log 2>/dev/null | sort | tail -n 1)
  [ -n "$f" ] || return 1
  [ ! -f "$d/.runner" ] || [ "$f" -nt "$d/.runner" ] || return 1
  grep -qi -e 'registration has been deleted' -e 'registration was not found' "$f"
}

alive() { [ -f "$RUN/$1.pid" ] && kill -0 "$(cat "$RUN/$1.pid")" 2>/dev/null; }

end_runner() {
  [ -f "$RUN/$1.pid" ] || return 0
  p=$(cat "$RUN/$1.pid")
  # the runner's own session: the setsid child and everything it started
  kill -TERM -- "-$p" 2>/dev/null || kill -TERM "$p" 2>/dev/null
  rm -f "$RUN/$1.pid" "$RUN/$1.since"
}

backoff_for() {
  c=0
  [ -f "$RUN/$1.fails" ] && c=$(cat "$RUN/$1.fails")
  d=2
  i=0
  while [ "$i" -lt "$c" ] && [ "$d" -lt 60 ]; do d=$((d * 2)); i=$((i + 1)); done
  [ "$d" -gt 60 ] && d=60
  echo "$d"
}

tick() {
  for f in "$STATE"/runners.d/*.env; do
    [ -e "$f" ] || continue
    n=$(basename "$f" .env)
    if [ -e "$STATE/runners.d/$n.stopped" ]; then
      end_runner "$n"
      continue
    fi
    if [ -e "$STATE/runners.d/$n.lost" ]; then
      end_runner "$n"
      continue
    fi
    if alive "$n"; then
      since=$(cat "$RUN/$n.since" 2>/dev/null || now)
      [ $(($(now) - since)) -ge 300 ] && rm -f "$RUN/$n.fails"
      continue
    fi
    # dead, or never started: wait out the back-off, then start it again
    if [ -f "$RUN/$n.next" ] && [ "$(now)" -lt "$(cat "$RUN/$n.next")" ]; then
      continue
    fi
    if [ -f "$RUN/$n.pid" ] && registration_lost "$n"; then
      # a deleted registration never comes back by restarting: stop here and say why
      echo "the registration of this runner was deleted at GitHub" >"$STATE/runners.d/$n.lost"
      echo "crsp supervisor: runner $n: its registration was deleted at GitHub; not starting it again until it registers again"
      rm -f "$RUN/$n.pid" "$RUN/$n.since" "$RUN/$n.next" "$RUN/$n.fails"
      continue
    fi
    if [ -f "$RUN/$n.pid" ]; then
      # the delay comes from the failures so far; the failure is counted after
      c=0
      [ -f "$RUN/$n.fails" ] && c=$(cat "$RUN/$n.fails")
      delay=$(backoff_for "$n")
      echo $((c + 1)) >"$RUN/$n.fails"
      echo "crsp supervisor: runner $n ended, starting it again in $delay s"
      echo $(($(now) + delay)) >"$RUN/$n.next"
      rm -f "$RUN/$n.pid"
      continue
    fi
    echo "crsp supervisor: starting runner $n"
    start_runner "$n" || echo "crsp supervisor: runner $n has no files yet"
  done
  # a runner whose file is gone
  for p in "$RUN"/*.pid; do
    [ -e "$p" ] || continue
    n=$(basename "$p" .pid)
    [ -e "$STATE/runners.d/$n.env" ] || end_runner "$n"
  done
}

term() { for p in "$RUN"/*.pid; do [ -e "$p" ] && end_runner "$(basename "$p" .pid)"; done; exit 0; }
trap term TERM INT

[ -n "$CRSP_SUPERVISOR_ONCE" ] && { tick; exit 0; }
while :; do
  tick
  if [ "$(self_sum)" != "$SELF_SUM" ] && idle; then
    echo "crsp supervisor: a newer copy of this script is in place, loading it"
    exec sh "$0"
  fi
  sleep "$TICK" &
  wait $!
done
