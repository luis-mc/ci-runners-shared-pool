package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/luis-mc/ci-runners-shared-pool/internal/store"
)

// dbSnapshot writes the user tables of the database at dbPath, read-only, as JSON (LC-10, LC-11: the lane
// compares the data the user keeps, not the bytes of a file the service rewrites each time it runs).
func dbSnapshot(dbPath, outPath string) error {
	data, err := store.UserData(context.Background(), dbPath)
	if err != nil {
		return err
	}
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return os.WriteFile(outPath, b, 0o600)
}

func loadSnapshot(path string) (map[string]store.TableData, error) {
	b, err := os.ReadFile(path) //nolint:gosec // a path the lane chose
	if err != nil {
		return nil, err
	}
	var m map[string]store.TableData
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return m, nil
}

// dbCompare checks that every row the before snapshot holds is still there, unchanged, in the after snapshot.
// A table or column that only the after snapshot has (a migration added it) is not a change to what was kept.
// The error names the first differences.
func dbCompare(beforePath, afterPath string) error {
	before, err := loadSnapshot(beforePath)
	if err != nil {
		return err
	}
	after, err := loadSnapshot(afterPath)
	if err != nil {
		return err
	}
	diffs := compareData(before, after)
	if len(diffs) == 0 {
		return nil
	}
	if len(diffs) > 5 {
		diffs = append(diffs[:5], fmt.Sprintf("and %d more", len(diffs)-5))
	}
	return fmt.Errorf("user data changed: %s", strings.Join(diffs, "; "))
}

func compareData(before, after map[string]store.TableData) []string {
	names := make([]string, 0, len(before))
	for n := range before {
		names = append(names, n)
	}
	sort.Strings(names)
	var diffs []string
	for _, n := range names {
		b := before[n]
		a, ok := after[n]
		if !ok {
			diffs = append(diffs, fmt.Sprintf("table %s is gone", n))
			continue
		}
		idx := make([]int, len(b.Columns))
		missing := ""
		for i, c := range b.Columns {
			idx[i] = -1
			for j, ac := range a.Columns {
				if ac == c {
					idx[i] = j
				}
			}
			if idx[i] < 0 {
				missing = c
			}
		}
		if missing != "" {
			diffs = append(diffs, fmt.Sprintf("table %s lost column %s", n, missing))
			continue
		}
		proj := make([]string, len(a.Rows))
		for r, row := range a.Rows {
			cells := make([]string, len(idx))
			for i, j := range idx {
				cells[i] = row[j]
			}
			proj[r] = strings.Join(cells, "\x00")
		}
		sort.Strings(proj)
		bw := make([]string, len(b.Rows))
		for r, row := range b.Rows {
			bw[r] = strings.Join(row, "\x00")
		}
		sort.Strings(bw)
		// Rows a migration or the running service added are not a loss; a before row that is gone or changed is.
		if lost := firstMissing(bw, proj); lost != "" {
			diffs = append(diffs, fmt.Sprintf("table %s: a row it had (%s) is gone or changed; %d rows before, %d after", n, rowSummary(lost), len(bw), len(proj)))
		}
	}
	return diffs
}

// firstMissing returns the first string of want (sorted) that got (sorted) does not hold.
func firstMissing(want, got []string) string {
	j := 0
	for _, w := range want {
		for j < len(got) && got[j] < w {
			j++
		}
		if j >= len(got) || got[j] != w {
			return w
		}
		j++
	}
	return ""
}

// rowSummary names a row by its first cell, so the line says where to look without printing the data.
func rowSummary(row string) string {
	first := strings.SplitN(row, "\x00", 2)[0]
	first = strings.TrimPrefix(first, "=")
	if len(first) > 40 {
		first = first[:40]
	}
	return "first column " + first
}
