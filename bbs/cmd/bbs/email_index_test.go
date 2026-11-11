package main

import (
	"strings"
	"testing"

	"gorm.io/gorm"
)

// Email lookups must use the NOCASE index. SQLite only does that when the query
// names the collation, so the assertion is on the plan for the SQL this code
// actually builds — not on a copy of the query written here.
func TestEmailLookupUsesTheNoCaseIndex(t *testing.T) {
	a := testApp(t)
	a.saveUser("EA7KLK", userProfile{FullName: "Volker", Email: "Volker@Example.ORG", Language: "en"})

	// Build the statement the real lookup would issue, without running it.
	statement := a.db.Session(&gorm.Session{DryRun: true}).Model(&dbUser{})
	query := emailLookup(statement, "volker@example.org").Find(&[]dbUser{}).Statement
	sql := query.SQL.String()
	if !strings.Contains(strings.ToUpper(sql), "COLLATE NOCASE") {
		t.Fatalf("the lookup query does not name the collation; it will scan: %s", sql)
	}

	var plan []struct{ Detail string }
	if err := a.db.Raw("EXPLAIN QUERY PLAN "+sql, query.Vars...).Scan(&plan).Error; err != nil {
		t.Fatal(err)
	}
	joined := ""
	for _, row := range plan {
		joined += row.Detail + "\n"
	}
	if !strings.Contains(joined, "idx_users_email_nocase") {
		t.Fatalf("the email lookup does not use the index:\n%s", joined)
	}

	// And the index really is collated, or a case-insensitive match would have to
	// scan anyway.
	var indexes []struct{ Name, SQL string }
	if err := a.db.Raw("SELECT name, sql FROM sqlite_master WHERE type='index' AND tbl_name='db_users' AND sql IS NOT NULL").Scan(&indexes).Error; err != nil {
		t.Fatal(err)
	}
	found := false
	for _, index := range indexes {
		if index.Name == "idx_users_email_nocase" {
			found = true
			if !strings.Contains(strings.ToLower(index.SQL), "collate nocase") {
				t.Fatalf("index is not collated: %s", index.SQL)
			}
		}
	}
	if !found {
		t.Fatal("idx_users_email_nocase was not created")
	}
}

// The lookup by address is case-insensitive and finds the account, which is what
// a password reset depends on: people type their address in whatever case.
func TestLookupAccountByEmailIsCaseInsensitive(t *testing.T) {
	a := testApp(t)
	a.saveUser("EA7KLK", userProfile{FullName: "Volker", Email: "Volker.Kerkhoff@Example.ORG", Language: "en"})

	for _, identifier := range []string{
		"volker.kerkhoff@example.org",
		"VOLKER.KERKHOFF@EXAMPLE.ORG",
		"  Volker.Kerkhoff@Example.Org  ",
	} {
		profile, callsign, found, err := a.lookupAccount(identifier)
		if err != nil {
			t.Fatalf("%q: %v", identifier, err)
		}
		if !found || callsign != "EA7KLK" || profile.Email == "" {
			t.Fatalf("lookupAccount(%q) = %q,%t", identifier, callsign, found)
		}
	}
	if _, _, found, _ := a.lookupAccount("nobody@example.org"); found {
		t.Fatal("lookupAccount found an account that does not exist")
	}
	// A callsign still resolves, and takes precedence over the email branch.
	if _, callsign, found, _ := a.lookupAccount("ea7klk"); !found || callsign != "EA7KLK" {
		t.Fatal("lookupAccount no longer resolves a callsign")
	}
}

// Legacy addresses stored with surrounding whitespace matched the old scan,
// which trimmed as it compared. The indexed equality does not, so the migration
// trims them once.
func TestMigrateTrimsLegacyStoredEmails(t *testing.T) {
	a := testApp(t)
	if err := a.db.Exec("INSERT INTO db_users (callsign, email) VALUES (?, ?)", "SPACY", "  spacy@example.org ").Error; err != nil {
		t.Fatal(err)
	}
	if err := migrateDatabase(a.db); err != nil {
		t.Fatal(err)
	}
	if _, callsign, found, err := a.lookupAccount("spacy@example.org"); err != nil || !found || callsign != "SPACY" {
		t.Fatalf("a legacy address did not match after migration: %q,%t,%v", callsign, found, err)
	}
	var stored string
	if err := a.db.Raw("SELECT email FROM db_users WHERE callsign = 'SPACY'").Scan(&stored).Error; err != nil {
		t.Fatal(err)
	}
	if stored != "spacy@example.org" {
		t.Fatalf("stored email = %q, want it trimmed", stored)
	}
}
