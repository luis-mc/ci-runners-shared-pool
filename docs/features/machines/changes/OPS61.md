---
change: minor
section: "5"
task: OPS61
---
The recommended pool size rests on the PC's own measured use: the agent keeps a rolling 7-day p95 of the memory used without the pool (available after 24 h), the room is total RAM x memory budget - p95 - a 1 GiB safety margin, and the Pool size block shows 'Windows and your apps use up to 10.2 GiB (p95, 7 days)' or 'measuring, N h of 24'. A Custom size that leaves the PC less than its p95 is a warning, not a block. On WSL2 the block shows the .wslconfig cap and the lines to copy; WSL must restart for a change to apply (R5.71).
