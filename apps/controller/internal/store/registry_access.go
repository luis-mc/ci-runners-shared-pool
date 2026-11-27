package store

import (
	"database/sql"
	"errors"

	"github.com/luis-mc/ci-runners-shared-pool/apps/controller/internal/store/sqlcgen"
)

// RegistryQueries gives the generated queries to the registry records adapter
// (internal/registry/records, registry R5.29 to R5.36). That package is not
// allowed to import database/sql, so the "no row" test is IsNoRows.
func (s *Store) RegistryQueries() *sqlcgen.Queries { return s.q }

// IsNoRows reports whether err is the answer of a query that found no row.
func IsNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }
