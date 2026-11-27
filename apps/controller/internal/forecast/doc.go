// Package forecast turns finished job durations into a time range, a run ETA and a queue start time.
// Pure functions: no clock, no network, no store. The caller reads the samples (jobsdata.Reader.Samples).
//
//  1. A sample is the duration of a successful job: the newest 30, none older than 90 days.
//  2. The same machine is preferred when it has at least 5 samples.
//  3. The point is the median; the range is p25 to p90 (nearest rank), never narrower than 10 percent each side.
//  4. Fewer than 5 samples: fall back job key, then workflow (same job type), then repository, else "not enough
//     history yet" (BasisNone, the sample count is kept).
//  5. Drift: with at least 15 samples, the median of the last 10 against the 20 before; a ratio above 1.25 or below
//     0.8 uses the last 10 and sets ChangedLately.
//  6. A run ETA is the longest path over the needs graph (CriticalPath): p25 to p75 summed along the longest paths.
//  7. Finished nodes have 0 remaining, running nodes the forecast minus the elapsed time (RemainingAfter), queued
//     nodes the whole forecast.
//  8. A queued job's start is found by simulating the slots (SimulateQueue), three times: lows, medians, highs.
//  9. A running job frees its slot at its remaining forecast; each queued job takes the first free slot in order.
//  10. A job without history counts as 0 s and is flagged unknown; the result says it is not complete.
//  11. RangeText and BasisText give the words the screens show; the UI labels every fallback.
//  12. Numbers are seconds, rounded to 0.1.
package forecast
