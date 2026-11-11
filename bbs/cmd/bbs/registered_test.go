package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"gorm.io/gorm"
)

// A new account records its registration date, and that date never moves.
func TestRegistrationRecordsDateOnce(t *testing.T) {
	a := testApp(t)
	installBasicText(a)
	a.showInfoHook = func(lang, title string, rows [][]string) {}

	queue := []struct {
		action string
		values map[string]string
		ok     bool
	}{
		{"submit", map[string]string{"callsign": "EA7KLK", "password": ""}, true},
		{"save", formValues(nil), true},
	}
	a.runFormHook = func(lang, title string, fields []formField, buttons []string) (string, map[string]string, bool) {
		next := queue[0]
		queue = queue[1:]
		return next.action, next.values, next.ok
	}

	_, profile, err := a.authenticate()
	if err != nil {
		t.Fatal(err)
	}
	if profile.Registered == "" {
		t.Fatal("registration did not record a date")
	}
	first := profile.Registered

	stored, ok, err := a.loadUser("EA7KLK")
	if err != nil || !ok {
		t.Fatalf("user not stored: ok=%v err=%v", ok, err)
	}
	if stored.Registered != first {
		t.Fatalf("stored registration date = %q, want %q", stored.Registered, first)
	}

	// Change the profile; the registration date must survive.
	stored.FullName = "Renamed"
	stored.LastSeen = "2027-01-01 00:00 UTC"
	if err := a.saveUser("EA7KLK", stored); err != nil {
		t.Fatal(err)
	}
	after, _, err := a.loadUser("EA7KLK")
	if err != nil {
		t.Fatal(err)
	}
	if after.Registered != first {
		t.Fatalf("registration date changed on update: %q -> %q", first, after.Registered)
	}
}

// Accounts created before the column existed get a date from first_seen, once.
func TestBackfillRegisteredFromFirstSeen(t *testing.T) {
	a := testApp(t)
	if err := a.db.Create(&dbUser{
		Callsign:  "EA7KLK",
		FirstSeen: "2026-07-10 12:00 UTC",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := a.db.Create(&dbUser{
		Callsign:   "DL1ABC",
		FirstSeen:  "2026-07-11 12:00 UTC",
		Registered: "2026-07-11 12:00 UTC",
	}).Error; err != nil {
		t.Fatal(err)
	}
	// A row with nothing to derive a date from stays empty rather than taking
	// a fabricated one.
	if err := a.db.Create(&dbUser{Callsign: "N0CALL"}).Error; err != nil {
		t.Fatal(err)
	}

	if err := backfillRegistered(a.db); err != nil {
		t.Fatal(err)
	}

	legacy, _, _ := a.loadUser("EA7KLK")
	if legacy.Registered != "2026-07-10 12:00 UTC" {
		t.Fatalf("backfilled date = %q", legacy.Registered)
	}
	already, _, _ := a.loadUser("DL1ABC")
	if already.Registered != "2026-07-11 12:00 UTC" {
		t.Fatalf("existing date was overwritten: %q", already.Registered)
	}
	empty, _, _ := a.loadUser("N0CALL")
	if empty.Registered != "" {
		t.Fatalf("fabricated a date for a row with no first_seen: %q", empty.Registered)
	}

	// Idempotent: running again changes nothing.
	if err := backfillRegistered(a.db); err != nil {
		t.Fatal(err)
	}
	again, _, _ := a.loadUser("EA7KLK")
	if again.Registered != legacy.Registered {
		t.Fatalf("second backfill changed the date: %q -> %q", legacy.Registered, again.Registered)
	}
}

func TestShortDate(t *testing.T) {
	cases := map[string]string{
		"2026-11-11 09:00 UTC": "2026-11-11",
		"2026-11-11":           "2026-11-11",
		"":                     "",
		"-":                    "-",
		"not a date":           "not a date",
	}
	for in, want := range cases {
		if got := shortDate(in); got != want {
			t.Fatalf("shortDate(%q) = %q, want %q", in, got, want)
		}
	}
}

// The sysop list carries a registered column whose value is the account's
// creation date, not its last-seen stamp.
func TestSysopUserListShowsRegistrationDate(t *testing.T) {
	a := testApp(t)
	a.text = map[string]map[string]any{"en": {
		"target_callsign": "Callsign",
		"date_registered": "Registered",
		"last_connection": "Last connection",
		"account_status":  "Status",
		"role":            "Role",
		"full_name":       "Name",
		"user_role":       "User",
		"sysop_role":      "Sysop",
		"enabled":         "Enabled",
		"menu_quit":       "Quit",
	}}
	users := map[string]userProfile{
		"EA7KLK": {
			FullName:   "Volker",
			Language:   "en",
			IsSysop:    true,
			Registered: "2026-03-04 10:00 UTC",
			LastSeen:   "2026-11-11 09:00 UTC",
		},
	}

	var header string
	var rows []string
	a.runMenuHook = func(lang, title, h string, opts []option) string {
		header = h
		for _, opt := range opts {
			if opt.value != "q" {
				rows = append(rows, opt.label)
			}
		}
		return "q"
	}

	if _, ok := a.chooseUser("en", users, ""); ok {
		t.Fatal("chooseUser reported a selection after quitting")
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1: %#v", len(rows), rows)
	}

	if !strings.Contains(header, "Registered") {
		t.Fatalf("header has no registered column: %q", header)
	}

	// The registered cell sits between the callsign and the last-seen cell, so
	// check it by position rather than by substring: the last-seen value also
	// contains a date, and only the column tells them apart.
	wantPrefix := paddedCell("EA7KLK", callsignWidth) + " " +
		paddedCell("2026-03-04", registeredWidth) + " " +
		paddedCell("2026-11-11 09:00 UTC", lastSeenWidth)
	if !strings.HasPrefix(rows[0], wantPrefix) {
		t.Fatalf("row prefix = %q, want %q", rows[0], wantPrefix)
	}

	// The header carries the same fixed columns as a row, so the two line up up
	// to the name, and neither can overflow the panel.
	wantHeaderPrefix := paddedCell("Callsign", callsignWidth) + " " +
		paddedCell("Registered", registeredWidth) + " " +
		paddedCell("Last connection", lastSeenWidth)
	if !strings.HasPrefix(header, wantHeaderPrefix) {
		t.Fatalf("header prefix = %q, want %q", header, wantHeaderPrefix)
	}
	for _, line := range []string{header, rows[0]} {
		if got := lipgloss.Width(line); got > panelContentWidth {
			t.Fatalf("line width %d exceeds the panel content width %d: %q", got, panelContentWidth, line)
		}
	}
}

// A missing date renders as a dash rather than a gap that shifts the columns.
func TestSysopUserListHandlesMissingRegistrationDate(t *testing.T) {
	row := userListRow("EA7KLK", "", "2026-11-11 09:00 UTC", "Enabled", "User", "Volker", 20)
	wantPrefix := paddedCell("EA7KLK", callsignWidth) + " " +
		paddedCell("-", registeredWidth) + " " +
		paddedCell("2026-11-11 09:00 UTC", lastSeenWidth) + " " +
		paddedCell("Enabled", statusWidth) + " " +
		paddedCell("User", roleWidth) + " " +
		"Volker"
	if row != wantPrefix {
		t.Fatalf("row = %q, want %q", row, wantPrefix)
	}
	// The fixed columns must not push the row past the 130-column panel.
	if got := lipgloss.Width(row); got > panelContentWidth {
		t.Fatalf("row width = %d exceeds the panel content width %d", got, panelContentWidth)
	}
}

// The user detail screen shows the registration date next to the account state.
func TestUserDetailRowsShowRegistrationDate(t *testing.T) {
	a := testApp(t)
	a.text = map[string]map[string]any{"en": {
		"date_registered": "Registered",
		"account_status":  "Status",
		"sysop":           "Sysop",
		"user_role":       "User",
		"sysop_role":      "Sysop",
		"enabled":         "Enabled",
		"full_name":       "Name",
		"email":           "Email",
		"maidenhead":      "Locator",
		"language":        "Language",
		"enable_aprs":     "APRS",
		"qth":             "QTH",
		"rig":             "Rig",
	}}
	profile := userProfile{FullName: "Volker", Language: "en", Registered: "2026-03-04 10:00 UTC"}
	rows := userDetailRows(a, "en", "EA7KLK", profile, false)

	found := false
	for _, row := range rows {
		if len(row) == 2 && row[0] == "Registered" {
			found = true
			if row[1] != "2026-03-04 10:00 UTC" {
				t.Fatalf("registered value = %q", row[1])
			}
		}
	}
	if !found {
		t.Fatalf("user detail rows have no registration date: %#v", rows)
	}
}

// The migration itself is exercised: opening a database adds and fills the
// column.
func TestOpenDatabaseAddsRegisteredColumn(t *testing.T) {
	a := testApp(t)
	// Simulate a pre-upgrade row: insert without the column value.
	if err := a.db.Exec("UPDATE db_users SET registered = '' WHERE 1=0").Error; err != nil {
		t.Fatalf("registered column missing: %v", err)
	}
	var row dbUser
	if err := a.db.Session(&gorm.Session{}).Where("1=1").Limit(1).Find(&row).Error; err != nil {
		t.Fatal(err)
	}
}
