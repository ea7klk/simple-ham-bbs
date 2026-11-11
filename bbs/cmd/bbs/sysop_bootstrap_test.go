package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// BBS_SYSOPS is a bootstrap grant only. These tests pin the rule: while the BBS
// has no active sysop, a configured callsign that logs in becomes one; once any
// sysop exists, the configured list confers nothing.

func TestConfiguredSysopsBootstrapWhenNoSysopExists(t *testing.T) {
	a := testApp(t)
	a.cfg.sysops = map[string]bool{"EA7KLK": true}

	users := map[string]userProfile{
		"EA7KLK": {FullName: "Volker", Email: "v@example.com", Language: "en"},
	}
	changed := a.applyConfiguredSysops(users)
	if len(changed) != 1 || changed[0] != "EA7KLK" {
		t.Fatalf("applyConfiguredSysops() = %v, want [EA7KLK]", changed)
	}
	if !users["EA7KLK"].IsSysop {
		t.Fatalf("bootstrap sysop not granted: %#v", users["EA7KLK"])
	}
}

func TestConfiguredSysopsIgnoredWhenASysopAlreadyExists(t *testing.T) {
	a := testApp(t)
	a.cfg.sysops = map[string]bool{"EA1ABC": true, "DL1ABC": true}

	users := map[string]userProfile{
		"EA7KLK": {FullName: "Volker", Language: "en", IsSysop: true},
		"EA1ABC": {FullName: "Alice", Language: "en"},
		"DL1ABC": {FullName: "Dieter", Language: "en"},
	}
	changed := a.applyConfiguredSysops(users)
	if len(changed) != 0 {
		t.Fatalf("applyConfiguredSysops() promoted %v while a sysop already existed", changed)
	}
	if users["EA1ABC"].IsSysop || users["DL1ABC"].IsSysop {
		t.Fatalf("configured callsigns gained sysop: %#v", users)
	}
}

// A disabled sysop is not an active one, so bootstrap is still allowed.
func TestConfiguredSysopsBootstrapWithOnlyDisabledSysops(t *testing.T) {
	a := testApp(t)
	a.cfg.sysops = map[string]bool{"EA7KLK": true}

	users := map[string]userProfile{
		"OLD":    {FullName: "Old", Language: "en", IsSysop: true, Disabled: true},
		"EA7KLK": {FullName: "Volker", Language: "en"},
	}
	changed := a.applyConfiguredSysops(users)
	if len(changed) != 1 || changed[0] != "EA7KLK" {
		t.Fatalf("applyConfiguredSysops() = %v, want [EA7KLK]", changed)
	}
}

func TestHasActiveSysop(t *testing.T) {
	cases := []struct {
		name  string
		users map[string]userProfile
		want  bool
	}{
		{"empty", map[string]userProfile{}, false},
		{"plain users", map[string]userProfile{"A": {}, "B": {}}, false},
		{"disabled sysop", map[string]userProfile{"A": {IsSysop: true, Disabled: true}}, false},
		{"active sysop", map[string]userProfile{"A": {IsSysop: true}}, true},
		{"mixed", map[string]userProfile{"A": {IsSysop: true, Disabled: true}, "B": {IsSysop: true}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasActiveSysop(tc.users); got != tc.want {
				t.Fatalf("hasActiveSysop() = %v, want %v", got, tc.want)
			}
		})
	}
}

// The original bug: an attacker registered a configured callsign and received
// the sysop menu. With a sysop in place, registration must produce a plain
// account.
func TestRegistrationDoesNotGrantSysopWhenOneExists(t *testing.T) {
	a := testApp(t)
	installBasicText(a)
	a.cfg.sysops = map[string]bool{"EA1ABC": true}
	if err := a.saveUser("EA7KLK", userProfile{FullName: "Volker", Email: "v@example.com", Language: "en", IsSysop: true, PasswordHash: hashPassword("ownerpass")}); err != nil {
		t.Fatal(err)
	}

	// An attacker claims the configured callsign and registers.
	queue := []struct {
		action string
		values map[string]string
		ok     bool
	}{
		{"submit", map[string]string{"callsign": "EA1ABC", "password": ""}, true},
		{"save", formValues(nil), true},
	}
	a.runFormHook = func(lang, title string, fields []formField, buttons []string) (string, map[string]string, bool) {
		next := queue[0]
		queue = queue[1:]
		return next.action, next.values, next.ok
	}
	a.showInfoHook = func(lang, title string, rows [][]string) {}

	callsign, profile, err := a.authenticate()
	if err != nil {
		t.Fatal(err)
	}
	if callsign != "EA1ABC" {
		t.Fatalf("authenticated as %q", callsign)
	}
	if profile.IsSysop {
		t.Fatalf("registration handed sysop to a configured callsign while a sysop existed: %#v", profile)
	}

	stored, ok, err := a.loadUser("EA1ABC")
	if err != nil || !ok {
		t.Fatalf("user not stored: ok=%v err=%v", ok, err)
	}
	if stored.IsSysop {
		t.Fatalf("stored account has sysop: %#v", stored)
	}
}

// With no sysop anywhere, the configured callsign is the intended bootstrap and
// does receive the role.
func TestRegistrationBootstrapsConfiguredSysop(t *testing.T) {
	a := testApp(t)
	installBasicText(a)
	a.cfg.sysops = map[string]bool{"EA7KLK": true}

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
	a.showInfoHook = func(lang, title string, rows [][]string) {}

	_, profile, err := a.authenticate()
	if err != nil {
		t.Fatal(err)
	}
	if !profile.IsSysop {
		t.Fatalf("bootstrap sysop was not granted: %#v", profile)
	}
}

// A bootstrapped sysop is an ordinary sysop afterwards: the configured list does
// not make them undemotable.
func TestBootstrappedSysopCanBeDemoted(t *testing.T) {
	a := testApp(t)
	installBasicText(a)
	a.cfg.sysops = map[string]bool{"EA7KLK": true}
	if err := a.replaceAllUsers(map[string]userProfile{
		"EA7KLK": {FullName: "Volker", Email: "v@example.com", Language: "en", IsSysop: true, PasswordHash: "h"},
		"EA1ABC": {FullName: "Alice", Email: "a@example.com", Language: "en", PasswordHash: "h"},
	}); err != nil {
		t.Fatal(err)
	}
	a.showInfoHook = func(lang, title string, rows [][]string) {}
	a.runMenuHook = func(lang, title, header string, opts []option) string { return "1" } // pick the first target

	users, err := a.loadUsers()
	if err != nil {
		t.Fatal(err)
	}
	var target string
	for _, key := range sortedKeys(users) {
		if key != "EA7KLK" {
			target = key
			break
		}
	}
	if target == "" {
		t.Fatal("no demotion target")
	}
	// Make the target the bootstrapped, formerly-configured sysop.
	p := users[target]
	p.IsSysop = true
	if err := a.saveUser(target, p); err != nil {
		t.Fatal(err)
	}
	users, _ = a.loadUsers()

	a.toggleSysop("EA7KLK", "en", users)
	after, _, err := a.loadUser(target)
	if err != nil {
		t.Fatal(err)
	}
	if after.IsSysop {
		t.Fatalf("bootstrapped sysop could not be demoted: %#v", after)
	}
}

// The last active sysop still cannot be demoted, however they got the role.
func TestLastSysopStillProtected(t *testing.T) {
	a := testApp(t)
	if err := a.replaceAllUsers(map[string]userProfile{
		"EA7KLK": {FullName: "Volker", Language: "en", IsSysop: true},
		"EA1ABC": {FullName: "Alice", Language: "en"},
	}); err != nil {
		t.Fatal(err)
	}
	users, _ := a.loadUsers()
	if !a.wouldRemoveLastSysop(users, "EA7KLK") {
		t.Fatal("last sysop is not protected")
	}
	if a.wouldRemoveLastSysop(users, "EA1ABC") {
		t.Fatal("a non-sysop was reported as the last sysop")
	}
}

// BBS_SYSOPS entries that never log in grant nothing at all.
func TestConfiguredSysopWithoutAccountIsNotGranted(t *testing.T) {
	a := testApp(t)
	a.cfg.sysops = map[string]bool{"N0CALL": true}
	users := map[string]userProfile{
		"EA7KLK": {FullName: "Volker", Language: "en"},
	}
	if changed := a.applyConfiguredSysops(users); len(changed) != 0 {
		t.Fatalf("applyConfiguredSysops() = %v, want none", changed)
	}
	if users["EA7KLK"].IsSysop {
		t.Fatal("unrelated account was promoted")
	}
}

// isBootstrapSysop fails closed when it cannot read the user table.
func TestIsBootstrapSysopFailsClosed(t *testing.T) {
	a := testApp(t)
	a.cfg.sysops = map[string]bool{"EA7KLK": true}
	if !a.isBootstrapSysop("EA7KLK") {
		t.Fatal("bootstrap should be available on an empty table")
	}
	if a.isBootstrapSysop("DL1ABC") {
		t.Fatal("a callsign outside BBS_SYSOPS was treated as bootstrap")
	}

	sqlDB, err := a.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
	if a.isBootstrapSysop("EA7KLK") {
		t.Fatal("isBootstrapSysop granted a role after the database was closed")
	}
}

// A stale user snapshot must not bootstrap a sysop when the database already
// has one: another session may have claimed the role after this one loaded.
func TestStaleSnapshotDoesNotBootstrapOverStoredSysop(t *testing.T) {
	a := testApp(t)
	a.cfg.sysops = map[string]bool{"EA1ABC": true}
	if err := a.saveUser("EA7KLK", userProfile{FullName: "Volker", Language: "en", IsSysop: true}); err != nil {
		t.Fatal(err)
	}
	stale := map[string]userProfile{
		"EA7KLK": {FullName: "Volker", Language: "en"}, // no sysop in this copy
		"EA1ABC": {FullName: "Alice", Language: "en"},
	}
	if changed := a.applyConfiguredSysops(stale); len(changed) != 0 {
		t.Fatalf("stale snapshot bootstrapped %v over a stored sysop", changed)
	}
}

// A sysop session is marked on every screen, not only on the main menu.
func TestBannerMarksSysopSessions(t *testing.T) {
	a := testApp(t)
	a.text = map[string]map[string]any{"en": {
		"current_user_label": "User",
		"sysop_role":         "Sysop",
	}}
	a.cfg.name = "HAMNET RADIO BBS"
	a.cfg.location = "HamNet"
	a.cfg.topic = "Tests"
	a.currentUser = "EA7KLK"

	a.currentUserIsSysop = false
	plain := a.banner("en")
	if strings.Contains(plain, "Sysop") {
		t.Fatalf("non-sysop banner carries the role marker: %q", plain)
	}

	a.currentUserIsSysop = true
	marked := a.banner("en")
	if !strings.Contains(marked, "EA7KLK") || !strings.Contains(marked, "Sysop") {
		t.Fatalf("sysop banner missing the role marker: %q", marked)
	}
}

// An operator who configures BBS_SYSOPS but never logs in with it has no sysop;
// startup records that state instead of leaving it invisible.
func TestBootstrapSysopStateIsReported(t *testing.T) {
	a := testApp(t)
	a.cfg.sysops = map[string]bool{"EA7KLK": true}
	a.cfg.bbsLogFile = t.TempDir() + "/bbs.log"

	// No account at all: the grant is still available.
	a.reportBootstrapSysopState()
	body, err := os.ReadFile(a.cfg.bbsLogFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "sysop_bootstrap_unclaimed") {
		t.Fatalf("missing unclaimed warning: %q", body)
	}

	// Account exists but has not logged in yet: the role arrives on next login.
	if err := a.saveUser("EA7KLK", userProfile{FullName: "Volker", Language: "en"}); err != nil {
		t.Fatal(err)
	}
	a.reportBootstrapSysopState()
	body, _ = os.ReadFile(a.cfg.bbsLogFile)
	if !strings.Contains(string(body), "sysop_bootstrap_pending") {
		t.Fatalf("missing pending warning: %q", body)
	}

	// A real sysop silences the warning.
	if err := a.saveUser("EA7KLK", userProfile{FullName: "Volker", Language: "en", IsSysop: true}); err != nil {
		t.Fatal(err)
	}
	before := len(body)
	a.reportBootstrapSysopState()
	body, _ = os.ReadFile(a.cfg.bbsLogFile)
	if len(body) != before {
		t.Fatalf("warning logged even though a sysop exists: %q", body)
	}
}

// An invalid BBS_SYSOPS entry fails startup instead of silently granting
// nothing.
func TestInvalidSysopsFailStartup(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BBS_DATA_DIR", dir)
	t.Setenv("BBS_DB_FILE", filepath.Join(dir, "bbs.sqlite"))
	t.Setenv("BBS_TRANSLATIONS_FILE", filepath.Join(dir, "missing.json"))
	t.Setenv("BBS_SYSOPS", "EA7KLK,not a callsign")

	if _, err := newApp(); err == nil {
		t.Fatal("newApp() accepted an invalid BBS_SYSOPS entry")
	} else if !strings.Contains(err.Error(), "NOT A CALLSIGN") {
		t.Fatalf("startup error does not name the bad entry: %v", err)
	}
}
