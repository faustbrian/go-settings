package postgres_test

import (
	"strings"
	"testing"

	"github.com/faustbrian/golib/pkg/settings/postgres"
)

func TestSchemaOwnsVersionedValuesAndImmutableHistory(t *testing.T) {
	t.Parallel()

	schema := postgres.Schema()
	for _, required := range []string{
		"CREATE TABLE IF NOT EXISTS settings_values",
		"PRIMARY KEY (scope_kind, scope_id, key_id)",
		"CREATE TABLE IF NOT EXISTS settings_history",
		"CREATE TABLE IF NOT EXISTS settings_migrations",
		"codec_version",
		"redacted",
	} {
		if !strings.Contains(schema, required) {
			t.Fatalf("schema missing %q", required)
		}
	}
}
