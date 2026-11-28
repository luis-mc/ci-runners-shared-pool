#!/bin/sh
# Adds one persistent runner to the pool container (runners R5.93):
#   add-runner.sh NAME URL LABELS GROUP KEEP   (the registration token on standard input)
# The runner folder is a copy of the runner in the image, kept in the state
# volume so the registration survives a restart of the container. It registers
# with `config.sh --unattended --replace` and never with --ephemeral. The token
# is read from standard input and passed to that one process in its environment
# only: it is in no file, no argument and no container setting (runners R6.2).
name=$1 url=$2 labels=$3 group=$4 keep=${5:-1}
STATE=${CRSP_STATE_DIR:-/state}
SRC=${CRSP_RUNNER_SRC:-/opt/actions-runner}
case "$name" in *[!A-Za-z0-9._-]* | "" | .*) echo "runner name $name cannot be used" >&2; exit 2 ;; esac
IFS= read -r ACTIONS_RUNNER_INPUT_TOKEN || exit 1
[ -n "$ACTIONS_RUNNER_INPUT_TOKEN" ] || exit 1
RUN=${CRSP_SUPERVISOR_RUN:-/tmp/crsp-supervisor}
dir=$STATE/$name
mkdir -p "$STATE/runners.d" "$STATE/busy" || exit 1
# One instance per runner name (OPS70): a second add of the same name ends the
# running listener first (the supervisor ends a runner whose entry file is gone),
# so two listeners never hold one name and a retry never starts a second one.
rm -f "$STATE/runners.d/$name.env"
i=0
while [ -f "$RUN/$name.pid" ] && kill -0 "$(cat "$RUN/$name.pid" 2>/dev/null)" 2>/dev/null; do
  i=$((i + 1))
  [ "$i" -gt "${CRSP_ADD_WAIT:-30}" ] && { echo "runner $name is still running" >&2; exit 1; }
  sleep 1
done
fresh=0
if [ ! -x "$dir/config.sh" ]; then
  fresh=1
  mkdir -p "$dir" && cp -a "$SRC/." "$dir/" || { rm -rf "$dir"; exit 1; }
fi
# Files of an old registration (deleted at GitHub) would make config.sh refuse
# or the listener use dead credentials: they go before the registration.
[ "$fresh" = 1 ] || rm -f "$dir/.runner" "$dir/.credentials" "$dir/.credentials_rsaparams"
set -- --unattended --replace --url "$url" --name "$name" --labels "$labels" --disableupdate --work _work
[ -n "$group" ] && set -- "$@" --runnergroup "$group"
if ! (export ACTIONS_RUNNER_INPUT_TOKEN && cd "$dir" && ./config.sh "$@"); then
  # only a folder this call made is removed: a failed retry never deletes a
  # runner that was already set up
  [ "$fresh" = 1 ] && rm -rf "$dir"
  exit 1
fi
if [ "$keep" = 0 ]; then k=0; else k=1; fi
printf 'CRSP_KEEP_DEPENDENCIES=%s\n' "$k" >"$dir/crsp-hygiene.env"
rm -f "$STATE/runners.d/$name.stopped" "$STATE/runners.d/$name.lost"
: >"$STATE/runners.d/$name.env"
