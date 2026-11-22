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
	version, err := store.UserDataVersion(context.Background(), dbPath)
	if err != nil {
		return err
	}
	b, err := json.Marshal(snapshot{SchemaVersion: version, Tables: data})
	if err != nil {
		return err
	}
	return os.WriteFile(outPath, b, 0o600)
}

// snapshot is what --db-snapshot writes: the user tables and the migration version they were read at, which
// decides whether a declared change (store.DeclaredChanges) applies between two snapshots.
type snapshot struct {
	SchemaVersion int64                      `json:"schema_version"`
	Tables        map[string]store.TableData `json:"tables"`
}

func loadSnapshot(path string) (snapshot, error) {
	b, err := os.ReadFile(path) //nolint:gosec // a path the lane chose
	if err != nil {
		return snapshot{}, err
	}
	var m snapshot
	if err := json.Unmarshal(b, &m); err != nil {
		return snapshot{}, fmt.Errorf("%s: %w", path, err)
	}
	if m.Tables == nil {
		return snapshot{}, fmt.Errorf("%s: no tables in the snapshot", path)
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
	bt, at, notes, diffs := applyDeclared(before, after, store.DeclaredChanges)
	for _, n := range notes {
		fmt.Println(n) // the lane writes each accepted declared change to lifecycle-steps.txt
	}
	diffs = append(diffs, compareData(bt, at)...)
	if len(diffs) == 0 {
		return nil
	}
	if len(diffs) > 5 {
		diffs = append(diffs[:5], fmt.Sprintf("and %d more", len(diffs)-5))
	}
	return fmt.Errorf("user data changed: %s", strings.Join(diffs, "; "))
}

// applyDeclared applies the declared changes whose migration lies between the two snapshots (before below it,
// after at or above it) and returns the tables left to compare, one note per accepted change, and the failures
// of a declaration that was not kept: a dropped table that is still there, even emptied.
// A dropped table leaves the comparison; a rewritten column is compared with its JSON key removed on both
// sides, so every row must still be there with the same other cells.
func applyDeclared(before, after snapshot, decls []store.DeclaredChange) (bt, at map[string]store.TableData, notes, diffs []string) {
	bt = make(map[string]store.TableData, len(before.Tables))
	for k, v := range before.Tables {
		bt[k] = v
	}
	at = make(map[string]store.TableData, len(after.Tables))
	for k, v := range after.Tables {
		at[k] = v
	}
	for _, d := range decls {
		if before.SchemaVersion >= d.Migration || after.SchemaVersion < d.Migration {
			continue
		}
		for _, t := range d.DropTables {
			if _, had := bt[t]; !had {
				continue
			}
			if _, still := at[t]; still {
				diffs = append(diffs, fmt.Sprintf("table %s is declared dropped by migration %d (%s) but is still there", t, d.Migration, d.Task))
				continue
			}
			delete(bt, t)
			notes = append(notes, fmt.Sprintf("LC-11 note: declared by migration %d (%s): table %s dropped", d.Migration, d.Task, t))
		}
		for _, r := range d.Rewrites {
			b, had := bt[r.Table]
			a, ok := at[r.Table]
			if !had || !ok {
				continue // a table that is gone is reported by the comparison
			}
			bt[r.Table] = withoutJSONKey(b, r.Column, r.JSONKey)
			at[r.Table] = withoutJSONKey(a, r.Column, r.JSONKey)
			notes = append(notes, fmt.Sprintf("LC-11 note: declared by migration %d (%s): table %s rewritten, key %s removed from column %s", d.Migration, d.Task, r.Table, r.JSONKey, r.Column))
		}
	}
	return bt, at, notes, diffs
}

// withoutJSONKey returns the table with the key removed from the JSON object in the column. A cell that is
// not a JSON object (or NULL) is left as it is.
func withoutJSONKey(td store.TableData, column, key string) store.TableData {
	ci := -1
	for i, c := range td.Columns {
		if c == column {
			ci = i
		}
	}
	if ci < 0 {
		return td
	}
	out := store.TableData{Columns: td.Columns, Rows: make([][]string, len(td.Rows))}
	for i, row := range td.Rows {
		r := append([]string(nil), row...)
		if ci < len(r) && strings.HasPrefix(r[ci], "=") {
			var obj map[string]json.RawMessage
			if json.Unmarshal([]byte(r[ci][1:]), &obj) == nil && obj != nil {
				delete(obj, key)
				if b, err := json.Marshal(obj); err == nil {
					r[ci] = "=" + string(b)
				}
			}
		}
		out.Rows[i] = r
	}
	return out
}

// runtimeColumns are columns the service rewrites by design on every save, by table. They are not config or data
// the user keeps: lifecycle.json names the data the user keeps (paths.data, keep_on_remove: ["data"]) and a save
// time is bookkeeping about the row, not a choice of the user. Only the named column is left out, never a table.
//   - repos_state.saved_at: rewritten to the save time by internal/store/reposstate.go SaveReposState
//     (ON CONFLICT ... saved_at = strftime(...'now')) each time the controller writes the repository state,
//     including the write at start. The column body, which holds the user's choices, is still compared.
var runtimeColumns = map[string]map[string]bool{
	"repos_state": {"saved_at": true},
}

// dropColumns returns the table without the columns that are runtime state.
func dropColumns(name string, td store.TableData) store.TableData {
	skip := runtimeColumns[name]
	if len(skip) == 0 {
		return td
	}
	keep := []int{}
	out := store.TableData{}
	for i, c := range td.Columns {
		if !skip[c] {
			keep = append(keep, i)
			out.Columns = append(out.Columns, c)
		}
	}
	for _, row := range td.Rows {
		r := make([]string, len(keep))
		for k, i := range keep {
			r[k] = row[i]
		}
		out.Rows = append(out.Rows, r)
	}
	return out
}

// changedColumns names the columns that differ between a lost before row and the after row that has the same
// first cell. Empty when the after table has no such row (the row is gone).
func changedColumns(cols []string, lost string, after [][]string) []string {
	want := strings.Split(lost, "\x00")
	for _, row := range after {
		if len(row) == 0 || len(want) == 0 || row[0] != want[0] {
			continue
		}
		var names []string
		for i, c := range cols {
			if i < len(row) && i < len(want) && row[i] != want[i] {
				names = append(names, c)
			}
		}
		return names
	}
	return nil
}

func compareData(before, after map[string]store.TableData) []string {
	names := make([]string, 0, len(before))
	for n := range before {
		names = append(names, n)
	}
	sort.Strings(names)
	var diffs []string
	for _, n := range names {
		b := dropColumns(n, before[n])
		a, ok := after[n]
		if ok {
			a = dropColumns(n, a)
		}
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
		projRows := make([][]string, len(a.Rows))
		for r, row := range a.Rows {
			cells := make([]string, len(idx))
			for i, j := range idx {
				cells[i] = row[j]
			}
			projRows[r] = cells
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
			what := ""
			if cols := changedColumns(b.Columns, lost, projRows); len(cols) > 0 {
				what = "; changed columns: " + strings.Join(cols, ", ")
			}
			diffs = append(diffs, fmt.Sprintf("table %s: a row it had (%s) is gone or changed; %d rows before, %d after%s", n, rowSummary(lost), len(bw), len(proj), what))
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
