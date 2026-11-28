#!/bin/sh
# One line per persistent runner of the pool container for the agent:
#   NAME|RUNNING|LOST   (RUNNING is 1 when the supervisor has the runner up, else 0;
#   LOST is the reason when GitHub deleted the runner's registration, else empty)
# Read only (runners R5.93, OPS70). A runner is in the list when its entry file
# is in runners.d, the same file the supervisor and report.sh go by.
STATE=${CRSP_STATE_DIR:-/state}
RUN=${CRSP_SUPERVISOR_RUN:-/tmp/crsp-supervisor}
for f in "$STATE"/runners.d/*.env; do
  [ -e "$f" ] || continue
  n=$(basename "$f" .env)
  up=0
  if [ ! -e "$STATE/runners.d/$n.stopped" ] && [ -f "$RUN/$n.pid" ] && kill -0 "$(cat "$RUN/$n.pid")" 2>/dev/null; then up=1; fi
  lost=
  # the supervisor's marker, or the listener log itself: an older supervisor
  # keeps starting the runner and writes no marker
  if [ -f "$STATE/runners.d/$n.lost" ]; then
    lost=$(head -n1 "$STATE/runners.d/$n.lost" | tr -d '|\r')
  else
    f=$(ls -1 "$STATE/$n/_diag"/Runner_*.log 2>/dev/null | sort | tail -n 1)
    if [ -n "$f" ] && { [ ! -f "$STATE/$n/.runner" ] || [ "$f" -nt "$STATE/$n/.runner" ]; } &&
      grep -qi -e 'registration has been deleted' -e 'registration was not found' "$f"; then
      lost="the registration of this runner was deleted at GitHub"
    fi
  fi
  printf '%s|%s|%s\n' "$n" "$up" "$lost"
done
