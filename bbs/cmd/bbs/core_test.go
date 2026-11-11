package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppHelpersAndSeedData(t *testing.T) {
	t.Setenv("BBS_TEST_ENV", "configured")
	if got := env("BBS_TEST_ENV", "fallback"); got != "configured" {
		t.Fatalf("env(configured) = %q", got)
	}
	if got := env("BBS_MISSING_ENV", "fallback"); got != "fallback" {
		t.Fatalf("env(fallback) = %q", got)
	}
	sysops := parseSysops(" ea7klk,EA1ABC,,ea7klk ")
	if !sysops["EA7KLK"] || !sysops["EA1ABC"] || len(sysops) != 2 {
		t.Fatalf("parseSysops() = %#v", sysops)
	}

	a := testApp(t)
	a.cfg.name = "HAMNET RADIO BBS"
	a.cfg.location = "HamNet"
	a.cfg.topic = "Tests"
	a.text = map[string]map[string]any{
		"en": {
			"hello":              "Hello",
			"items":              []any{"one", "two"},
			"current_user_label": "User",
		},
		"de": {"hello": "Hallo"},
	}
	if got := a.t("de", "hello"); got != "Hallo" {
		t.Fatalf("translated t() = %q", got)
	}
	if got := a.t("fr", "hello"); got != "Hello" {
		t.Fatalf("fallback t() = %q", got)
	}
	if got := a.tList("fr", "items"); len(got) != 2 || got[0] != "one" || got[1] != "two" {
		t.Fatalf("tList fallback = %#v", got)
	}
	a.currentUser = "EA7KLK"
	if banner := a.banner("en"); !strings.Contains(banner, "EA7KLK") || !strings.Contains(banner, "HamNet") {
		t.Fatalf("banner missing expected content: %q", banner)
	}

	if err := a.seedData(); err != nil {
		t.Fatal(err)
	}
	bulletins, err := a.loadBulletins()
	if err != nil {
		t.Fatal(err)
	}
	if len(bulletins) == 0 {
		t.Fatal("seedData did not create default bulletins")
	}
	boards, err := a.loadBoards()
	if err != nil {
		t.Fatal(err)
	}
	if len(boards.Boards) != 1 || boards.Boards[0].ID != defaultBoardID {
		t.Fatalf("seedData boards = %#v", boards.Boards)
	}
}

func TestStorageUsersPasswordsAndJSON(t *testing.T) {
	a := testApp(t)
	users := map[string]userProfile{
		"ea7klk": {FullName: "Volker", Email: "v@example.com", Language: "de", EnableAPRS: true, IsSysop: true, LastSeen: "2026-07-10 10:00 UTC"},
		"empty":  {FullName: "skip"},
	}
	if err := a.replaceAllUsers(users); err != nil {
		t.Fatal(err)
	}
	loaded, err := a.loadUsers()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 2 || !loaded["EA7KLK"].EnableAPRS || loaded["EA7KLK"].Language != "de" {
		t.Fatalf("loadUsers() = %#v", loaded)
	}
	delete(loaded, "EMPTY")
	if err := a.replaceAllUsers(loaded); err != nil {
		t.Fatal(err)
	}
	loaded, _ = a.loadUsers()
	if _, ok := loaded["EMPTY"]; ok {
		t.Fatalf("replaceAllUsers did not replace the user table: %#v", loaded)
	}

	p := loaded["EA7KLK"]
	p.IsSysop = false
	loaded["EA7KLK"] = p
	// Persist the demotion so the bootstrap check sees a database with no active
	// sysop, which is the state BBS_SYSOPS is allowed to fill.
	if err := a.saveUser("EA7KLK", p); err != nil {
		t.Fatal(err)
	}
	a.cfg.sysops = map[string]bool{"EA1ABC": true, "EA7KLK": true}
	if changed := a.applyConfiguredSysops(loaded); len(changed) != 1 || changed[0] != "EA7KLK" {
		t.Fatalf("applyConfiguredSysops() = %v, want [EA7KLK]", changed)
	}
	if !a.isSysop(loaded["EA7KLK"]) {
		t.Fatal("configured/profile sysop not detected")
	}
	if !a.wouldRemoveLastSysop(map[string]userProfile{"EA7KLK": {IsSysop: true}}, "EA7KLK") {
		t.Fatal("wouldRemoveLastSysop failed for single active sysop")
	}
	if a.wouldRemoveLastSysop(map[string]userProfile{"EA7KLK": {IsSysop: true, Disabled: true}}, "EA7KLK") {
		t.Fatal("disabled sysop should not be considered removable last active sysop")
	}

	hash := hashPassword("secret123")
	if hash == "" || !verifyPassword("secret123", hash) || verifyPassword("wrong", hash) {
		t.Fatalf("password verification failed for hash %q", hash)
	}
	for _, stored := range []string{"", "bad$hash", "pbkdf2_sha256$x$salt$digest", "pbkdf2_sha256$1$**$digest", "pbkdf2_sha256$1$c2FsdA==$**"} {
		if verifyPassword("secret123", stored) {
			t.Fatalf("verifyPassword accepted invalid stored hash %q", stored)
		}
	}

	dir := t.TempDir()
	var target map[string]string
	if err := readJSON(filepath.Join(dir, "missing.json"), &target, map[string]string{"fallback": "yes"}); err != nil {
		t.Fatal(err)
	}
	if target["fallback"] != "yes" {
		t.Fatalf("readJSON missing fallback = %#v", target)
	}
	path := filepath.Join(dir, "data.json")
	data, _ := json.Marshal(map[string]string{"ok": "true"})
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := readJSON(path, &target, nil); err != nil {
		t.Fatal(err)
	}
	if target["ok"] != "true" || !exists(path) || exists(filepath.Join(dir, "nope")) {
		t.Fatalf("readJSON/exists unexpected target=%#v", target)
	}
	// A malformed file still falls back, but must report the parse error so the
	// caller can log it instead of silently serving raw keys.
	if err := os.WriteFile(path, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	parseErr := readJSON(path, &target, map[string]string{"bad": "fallback"})
	if parseErr == nil {
		t.Fatal("readJSON swallowed a malformed JSON file")
	}
	if !strings.Contains(parseErr.Error(), path) {
		t.Fatalf("readJSON error does not name the file: %v", parseErr)
	}
	if target["bad"] != "fallback" {
		t.Fatalf("readJSON invalid fallback = %#v", target)
	}
}

func TestProfileAndUtilityHelpers(t *testing.T) {
	a := testApp(t)
	a.text = map[string]map[string]any{"en": {
		"full_name":       "Full name",
		"email":           "Email",
		"maidenhead":      "Locator",
		"language":        "Language",
		"enable_aprs":     "Enable APRS",
		"qth":             "QTH",
		"rig":             "Rig",
		"new_password":    "New password",
		"verify_password": "Verify password",
	}}
	profile := userProfile{FullName: "Name", Email: "n@example.com", Maidenhead: "im77ah", Language: "en", EnableAPRS: true, QTH: "QTH", Rig: "Rig", PasswordHash: "hash"}
	if !profileComplete(profile) {
		t.Fatal("profileComplete returned false for complete profile")
	}
	fields := a.profileFields(profile, true, true)
	if len(fields) != 9 || fields[2].normalizer("im77ah") != "IM77ah" {
		t.Fatalf("profileFields unexpected fields: %#v", fields)
	}
	updated := applyProfileValues(userProfile{}, map[string]string{
		"full_name": "New", "email": "new@example.com", "maidenhead": "IM77AH", "language": "fr", "enable_aprs": "true", "qth": "Home", "rig": "HT",
	})
	if updated.Language != "fr" || !updated.EnableAPRS || updated.QTH != "Home" {
		t.Fatalf("applyProfileValues() = %#v", updated)
	}
	rows := profileRows(a, "en", updated)
	if len(rows) < 7 || rows[0][1] != "New" {
		t.Fatalf("profileRows() = %#v", rows)
	}

	if normalizeCallsign(" ea7klk ") != "EA7KLK" || normalizeLocator("im77ah") != "IM77ah" {
		t.Fatal("normalizers returned unexpected values")
	}
	if got := len(languageOptions()); got != len(languageOrder) {
		t.Fatalf("languageOptions count = %d", got)
	}
	if boolString(true) != "true" || boolString(false) != "false" {
		t.Fatal("boolString failed")
	}
	choices := []option{{"a", "A"}, {"b", "B"}, {"c", "C"}}
	if choiceStep(choices, "a", -1) != "c" || choiceStep(choices, "b", 1) != "c" || choiceStep(nil, "x", 1) != "x" {
		t.Fatal("choiceStep failed")
	}
	if !contains([]string{"a", "b"}, "b") || contains([]string{"a"}, "x") {
		t.Fatal("contains failed")
	}
	if buttonLabelKey("login") != "login_button" || buttonLabelKey("unknown") != "cancel_button" {
		t.Fatal("buttonLabelKey failed")
	}
	if boardID("APRS Traffic!") != "aprs-traffic" || boardID("!!!") != defaultBoardID || len(boardID(strings.Repeat("a", 80))) != 40 {
		t.Fatal("boardID failed")
	}
	if clip("  abcdef  ", 3) != "abc" || firstNonEmpty("", nil, "ok") != "ok" {
		t.Fatal("clip/firstNonEmpty failed")
	}
	if asciiSafe("Aé\nB") != "A??B" {
		t.Fatal("asciiSafe failed")
	}
	if replySubject("Hello") != "Re: Hello" || replySubject("re: Hello") != "re: Hello" {
		t.Fatal("replySubject failed")
	}
}

func TestLoggingWritesFile(t *testing.T) {
	dir := t.TempDir()
	a := &app{cfg: config{bbsLogFile: filepath.Join(dir, "bbs.log")}}
	appendLogFile(filepath.Join(dir, "plain.log"), "plain\n")
	a.logBBSAction("ea7klk", "login", "sysop=%t", true)
	body, err := os.ReadFile(a.cfg.bbsLogFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "action=login user=EA7KLK sysop=true") {
		t.Fatalf("BBS log missing action: %s", body)
	}
}

// A corrupt translations file must leave a trace instead of silently serving
// raw keys.
func TestMalformedTranslationsIsLogged(t *testing.T) {
	dir := t.TempDir()
	transPath := filepath.Join(dir, "translations.json")
	if err := os.WriteFile(transPath, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "bbs.log")

	t.Setenv("BBS_TRANSLATIONS_FILE", transPath)
	t.Setenv("BBS_DATA_DIR", dir)
	t.Setenv("BBS_DB_FILE", filepath.Join(dir, "bbs.sqlite"))
	t.Setenv("BBS_AUTH_LOG_FILE", filepath.Join(dir, "auth.log"))

	a, err := newApp()
	if err != nil {
		t.Fatalf("newApp() refused to start on a malformed translations file: %v", err)
	}
	if a == nil {
		t.Fatal("newApp() returned no app")
	}

	body, readErr := os.ReadFile(logPath)
	if readErr != nil {
		t.Fatalf("no log file written: %v", readErr)
	}
	if !strings.Contains(string(body), "translations_parse_error") || !strings.Contains(string(body), transPath) {
		t.Fatalf("parse error not logged: %q", body)
	}
}
