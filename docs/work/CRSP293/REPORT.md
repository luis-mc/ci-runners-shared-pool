# CRSP293 report
- Cause: TestDepsCheckRoutesTheReport answered from a goroutine that polled 200 runtime.Gosched() calls. On the slow macOS runner the polls ended before SendCheck sent; nothing answered; SendCheck waited DefaultTimeout (15 min); the 10 s package timeout (scripts/ci/go-test.sh unit tier) killed the package, so no test name in the log.
- Fix: poll by clock (5 s, 1 ms sleeps); SendCheck gets an 8 s context so a miss fails the test fast.
- Local: go test -count=20 with GOMAXPROCS=1 green.
- Next: CI on branch.
