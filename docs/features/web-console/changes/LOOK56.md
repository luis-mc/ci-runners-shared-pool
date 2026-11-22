---
change: major
task: LOOK56
---
Console layout (owner decision 22 Nov 2026, LOOK54): the top menu is Overview, Setup, Pool, Doctor, Analytics, Settings, Help. Setup holds configuration (Summary, Machines, Runners, Images and caches, Size and backends, Pool setup, the old Repositories screen); Pool shows what runs (Overview, Live, Queue Analysis, Jobs, Machines) and is realtime (stream events, 1 s timers, a 5 s poll while the stream is down); Analytics holds Suggestions (each with its repository and numbered steps), Insights, Charts and Run elsewhere; Doctor opens on Needs you; Help has a side menu. The old addresses are removed with no redirects. Links use theme tokens in every state (UX-80), the page frame is 96rem with wide layouts from 1400 px (UX-79), and the header status wraps in its own column at phone width.
