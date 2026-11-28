---
change: patch
task: CRSP381
---
The key-hint bar height is also read on window resize and when fonts load, so the sticky action bar never lags it by a frame.
The sticky action bar is drawn right after the main column, as a child of the frame: a tall header, menu or banner above the main column no longer holds it over the key-hint bar at the top of a page on a phone.
