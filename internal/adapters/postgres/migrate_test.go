package postgres

import "testing"

func TestMigrationVersion(t *testing.T) {
	version, err := migrationVersion("000123_add_orders.sql")
	if err != nil {
		t.Fatalf("migrationVersion() error = %v", err)
	}
	if version != 123 {
		t.Errorf("migrationVersion() = %d, want 123", version)
	}
}

func TestMigrationVersionRejectsInvalidNames(t *testing.T) {
	for _, name := range []string{"schema.sql", "invalid_name.sql", "000000_zero.sql"} {
		t.Run(name, func(t *testing.T) {
			if _, err := migrationVersion(name); err == nil {
				t.Fatalf("migrationVersion(%q) error = nil, want error", name)
			}
		})
	}
}

func TestMigrationChecksumChangesWithContents(t *testing.T) {
	first := migrationChecksum([]byte("CREATE TABLE example (id TEXT);"))
	second := migrationChecksum([]byte("CREATE TABLE example (id BIGINT);"))
	if first == second {
		t.Fatalf("migration checksums are equal for different SQL: %q", first)
	}
	if len(first) != 64 {
		t.Fatalf("migration checksum length = %d, want 64", len(first))
	}
}
