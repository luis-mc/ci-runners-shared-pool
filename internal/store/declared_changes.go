package store

// DeclaredChange is what one migration removes or rewrites on purpose in data the user keeps. The lifecycle
// lane (LC-11, upgrade from the previous release) accepts exactly these changes, and only across the migration
// that makes them: the before database must be below Migration and the after database at or above it. Every
// other lost table, lost row or changed cell still fails LC-11. A migration that drops a table or rewrites rows
// adds its entry here in the same change.
type DeclaredChange struct {
	Migration  int64             // the migration number (the NNNNN of NNNNN_name.sql)
	Task       string            // the task that made the change
	Reason     string            // why the data goes or changes
	DropTables []string          // tables the migration drops; each must be fully gone, not emptied
	Rewrites   []DeclaredRewrite // tables whose rows the migration rewrites
}

// DeclaredRewrite names a rewrite that removes keys from the JSON object in one column of a table. Every row must
// still be there, with the same other cells; the column is compared with exactly those keys removed on both sides.
// A path is "$" followed by dot-separated object keys; "*" stands for every member of an object, so
// "$.policies.*.legacy_vars" is the key legacy_vars of each policy under policies.
type DeclaredRewrite struct {
	Table     string
	Column    string
	JSONPaths []string
}

// DeclaredChanges lists the declared changes, by migration number.
var DeclaredChanges = []DeclaredChange{
	{
		Migration:  42,
		Task:       "CRSP227",
		Reason:     "parity removed",
		DropTables: []string{"access_credentials"},
	},
	{
		Migration: 43,
		Task:      "CRSP229",
		Reason:    "legacy_vars removed from every saved policy",
		Rewrites: []DeclaredRewrite{{Table: "repos_state", Column: "body", JSONPaths: []string{
			"$.policies.*.LegacyVars", "$.policies.*.legacy_vars",
		}}},
	},
}
