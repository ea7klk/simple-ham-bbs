package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// One-shot initialisation.
//
// AutoMigrate and the seed/backfill sweep used to run in openDatabase, which
// every SSH session calls. Each session is its own process, so the cost was paid
// per connection and grew with the user count (SCALABILITY.md). These tests pin
// the new arrangement: the session path opens and waits, and the migration runs
// once.

// Opening a database must not create the schema. That is the whole point: a
// session opens it, and nothing expensive may happen there.
func TestOpenDatabaseDoesNotMigrate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fresh.sqlite")
	db, err := openDatabase(path)
	if err != nil {
		t.Fatal(err)
	}
	var tables int64
	if err := db.Raw("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name LIKE 'db_%'").Scan(&tables).Error; err != nil {
		t.Fatal(err)
	}
	if tables != 0 {
		t.Fatalf("openDatabase created %d table(s); the session path must not migrate", tables)
	}
	if schemaReady(db) {
		t.Fatal("openDatabase reported a ready schema on an empty file")
	}
}

// migrateDatabase does the work and records that it did.
func TestMigrateDatabaseCreatesTheSchemaAndRecordsIt(t *testing.T) {
	db, err := openDatabase(filepath.Join(t.TempDir(), "fresh.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if schemaReady(db) {
		t.Fatal("schema reported ready before migrating")
	}
	if err := migrateDatabase(db); err != nil {
		t.Fatal(err)
	}
	if !schemaReady(db) {
		t.Fatal("schema not ready after migrating")
	}
	for _, table := range []string{"db_users", "db_messages", "db_boards", "db_tokens", "db_settings"} {
		var count int64
		if err := db.Raw("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name = ?", table).Scan(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("table %s was not created", table)
		}
	}
}

// Running it again must be harmless: the supervisor and the container entrypoint
// can both reach it, and an operator may run `bbs_app migrate` by hand.
func TestMigrateDatabaseIsIdempotent(t *testing.T) {
	a := testApp(t)
	// testApp has already migrated. A second and third run must change nothing.
	for i := 0; i < 3; i++ {
		if err := migrateDatabase(a.db); err != nil {
			t.Fatalf("run %d: %v", i+2, err)
		}
	}
	if !schemaReady(a.db) {
		t.Fatal("schema not ready after repeated migration")
	}
}

// initialize is the one-shot wrapper: migrate, then seed and report.
func TestInitializeSeedsAndReports(t *testing.T) {
	dir := t.TempDir()
	db, err := openDatabase(filepath.Join(dir, "bbs.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	a := &app{cfg: config{
		dataDir:     dir,
		dbFile:      filepath.Join(dir, "bbs.sqlite"),
		aprsLogFile: filepath.Join(dir, "aprs", "aprs.log"),
		bbsLogFile:  filepath.Join(dir, "bbs.log"),
		authLogFile: filepath.Join(dir, "auth.log"),
	}, db: db, text: map[string]map[string]any{"en": {}}, mail: &mailer{now: time.Now}}

	if err := a.initialize(); err != nil {
		t.Fatal(err)
	}
	if !schemaReady(a.db) {
		t.Fatal("initialize did not leave the schema ready")
	}
	bulletins, err := a.loadBulletins()
	if err != nil {
		t.Fatal(err)
	}
	if len(bulletins) == 0 {
		t.Fatal("initialize did not seed the default bulletins")
	}
	boards, err := a.loadBoards()
	if err != nil {
		t.Fatal(err)
	}
	if len(boards.Boards) == 0 {
		t.Fatal("initialize did not seed the default board")
	}
}

// A session against an unmigrated file must say so rather than failing on a
// missing table, and must not silently migrate it itself.
func TestWaitForSchemaRefusesAnUnmigratedDatabase(t *testing.T) {
	db, err := openDatabase(filepath.Join(t.TempDir(), "fresh.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	a := &app{cfg: config{bbsLogFile: filepath.Join(t.TempDir(), "bbs.log")}, db: db}

	original := schemaWait
	schemaWait = 150 * time.Millisecond
	defer func() { schemaWait = original }()

	if err := a.waitForSchema(); err == nil {
		t.Fatal("waitForSchema accepted an unmigrated database")
	} else if !strings.Contains(err.Error(), "migrate") {
		t.Fatalf("error does not point at the fix: %v", err)
	}
	// And it really did not migrate: a session must not take the write lock.
	if schemaReady(db) {
		t.Fatal("waitForSchema migrated the database itself")
	}
}

// ...and it returns as soon as the migration has happened.
func TestWaitForSchemaSucceedsOnceMigrated(t *testing.T) {
	a := testApp(t)
	if err := a.waitForSchema(); err != nil {
		t.Fatalf("waitForSchema on a migrated database: %v", err)
	}
}

// A table added without a version bump must still be created: the marker records
// that a migration ran, not that this build's migration ran.
func TestMissingTableIsRepairedDespiteCurrentMarker(t *testing.T) {
	a := testApp(t)

	// Simulate the state that shipped in v1.30: marker says current, a table is
	// absent because the version was never bumped.
	if err := a.db.Migrator().DropTable(&dbAPRSBulletin{}); err != nil {
		t.Fatal(err)
	}
	// The marker already exists from the test app's initialisation; make sure it
	// reads as current regardless of version.
	if err := a.db.Model(&dbSetting{}).Where("key = ?", schemaVersionKey).
		UpdateColumn("value", schemaVersion).Error; err != nil {
		t.Fatal(err)
	}
	if !schemaReady(a.db) {
		t.Fatal("precondition: marker should read as current")
	}
	if missing := missingTables(a.db); len(missing) == 0 {
		t.Fatal("missingTables did not notice the dropped table")
	} else {
		t.Logf("detected missing: %v", missing)
	}

	if err := a.initialize(); err != nil {
		t.Fatal(err)
	}
	if !a.db.Migrator().HasTable(&dbAPRSBulletin{}) {
		t.Fatal("initialize did not recreate the missing table")
	}
	if missing := missingTables(a.db); len(missing) != 0 {
		t.Fatalf("still missing after initialize: %v", missing)
	}
}

// Every model the migration claims to create is actually created.
func TestSchemaModelsAreAllCreated(t *testing.T) {
	a := testApp(t)
	if missing := missingTables(a.db); len(missing) != 0 {
		t.Fatalf("migration did not create: %v", missing)
	}
	if len(schemaModels()) == 0 {
		t.Fatal("schemaModels is empty")
	}
}
