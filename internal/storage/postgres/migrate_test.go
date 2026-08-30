package postgres

import (
	"testing"
	"testing/fstest"
)

func TestEmbeddedMigrationsAreValid(t *testing.T) {
	items, err := loadMigrations(migrationFiles)
	if err != nil {
		t.Fatalf("loadMigrations() error = %v", err)
	}
	if len(items) == 0 || items[0].Version != 1 {
		t.Fatalf("migrations = %#v", items)
	}
	if items[0].Checksum == "" || items[0].SQL == "" {
		t.Fatal("migration must have SQL and checksum")
	}
}

func TestLoadMigrationsRejectsDuplicateVersions(t *testing.T) {
	source := fstest.MapFS{
		"migrations/000001_one.sql": {Data: []byte("SELECT 1")},
		"migrations/000001_two.sql": {Data: []byte("SELECT 2")},
	}
	if _, err := loadMigrations(source); err == nil {
		t.Fatal("expected duplicate migration error")
	}
}

func TestLoadMigrationsSortsByVersion(t *testing.T) {
	source := fstest.MapFS{
		"migrations/000010_ten.sql": {Data: []byte("SELECT 10")},
		"migrations/000002_two.sql": {Data: []byte("SELECT 2")},
	}
	items, err := loadMigrations(source)
	if err != nil {
		t.Fatal(err)
	}
	if items[0].Version != 2 || items[1].Version != 10 {
		t.Fatalf("versions = %d, %d", items[0].Version, items[1].Version)
	}
}
