---
change: minor
task: OPS96
---
Windows registers and removes a runner by calling Runner.Listener.exe directly with no standard input, the token only in the secret environment and its output read by the agent. A configure or remove that runs past 5 minutes is stopped and the add fails with that reason and the listener's last output; a leftover process holding the output no longer keeps the step waiting (R5.87).
