package store

import (
	"math"
	"testing"
	"time"
)

// unixMillis answers what time.Parse answers, for every kind of day the calendar has.
func TestUnixMillisMatchesParse(t *testing.T) {
	for _, s := range []string{
		"1970-01-01T00:00:00.000Z", "1969-12-31T23:59:59.999Z", "2000-02-29T12:30:45.123Z", "2100-03-01T00:00:00.000Z",
		"2024-02-29T23:59:59.999Z", "2026-11-27T15:04:05.007Z", "1600-01-01T00:00:00.000Z", "2026-08-29T13:13:34Z", "2024-02-29T23:59:59Z",
	} {
		got, ok := unixMillis(s)
		if !ok || got != parseTime(s).UnixMilli() {
			t.Errorf("%s: got %d %v, want %d", s, got, ok, parseTime(s).UnixMilli())
		}
	}
	for day := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC); day.Year() < 2030; day = day.Add(37*time.Hour + 11*time.Minute + 3*time.Second + 17*time.Millisecond) {
		if got, ok := unixMillis(fmtTime(day)); !ok || got != day.UnixMilli() {
			t.Fatalf("%s: got %d %v", fmtTime(day), got, ok)
		}
	}
	for _, bad := range []string{"", "2026-11-27T15:04:05z", "2026-11-27 15:04:05.000Z", "2026-13-27T15:04:05.000Z", "2026-11-27T15:04:0x.000Z", "2026-11-27T25:04:05.000Z"} {
		if _, ok := unixMillis(bad); ok {
			t.Errorf("%q should not parse", bad)
		}
	}
}

func TestSecondsBetweenMatchesSeconds(t *testing.T) {
	cases := [][2]string{
		{"2026-11-27T10:00:00.000Z", "2026-11-27T10:00:01.500Z"},
		{"2026-11-27T10:00:01.500Z", "2026-11-27T10:00:00.000Z"},
		{"", "2026-11-27T10:00:00.000Z"},
		{"2026-11-27T10:00:00Z", "2026-11-27T10:01:00.250Z"},
		{"2026-11-27T10:00:00+01:00", "2026-11-27T10:01:00Z"}, // another layout falls back to time.Parse
		{"2026-11-27T10:00:00.000Z", "2026-11-27T10:00:00.000Z"},
	}
	for _, c := range cases {
		want, wok := seconds(c[0], c[1])
		got, ok := secondsBetween(c[0], c[1])
		if ok != wok || math.Abs(got-want) > 1e-9 {
			t.Errorf("%v: got %v %v, want %v %v", c, got, ok, want, wok)
		}
	}
}
