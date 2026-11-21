#!/bin/sh
# One line per persistent runner of the pool container for the agent:
#   NAME|RUNNING   (RUNNING is 1 when the supervisor has the runner up, else 0)
# Read only (runners R5.93, OPS70). A runner is in the list when its entry file
# is in runners.d, the same file the supervisor and report.sh go by.
STATE=${CRSP_STATE_DIR:-/state}
RUN=${CRSP_SUPERVISOR_RUN:-/tmp/crsp-supervisor}
for f in "$STATE"/runners.d/*.env; do
  [ -e "$f" ] || continue
  n=$(basename "$f" .env)
  up=0
  if [ ! -e "$STATE/runners.d/$n.stopped" ] && [ -f "$RUN/$n.pid" ] && kill -0 "$(cat "$RUN/$n.pid")" 2>/dev/null; then up=1; fi
  printf '%s|%s\n' "$n" "$up"
done
