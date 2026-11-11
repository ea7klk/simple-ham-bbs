package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
)

// APRS bulletins: parsing, categorisation, dedupe, retention and screens.

func TestParseBulletinAddress(t *testing.T) {
	cases := []struct {
		name    string
		to      string
		wantOK  bool
		wantSl  int
		wantGrp string
	}{
		{"slot and five-char group", "BLN1NWS", true, 1, "NWS"},
		{"slot 5", "BLN5SKYW", true, 5, "SKYW"},
		{"lowercase", "bln2arrl", true, 2, "ARRL"},
		{"no group", "BLN3", true, 3, ""},
		{"padded field", "BLN1NWS  ", true, 1, "NWS"},
		{"slot 0", "BLN0TEST", true, 0, "TEST"},
		{"group of one", "BLN1X", true, 1, "X"},
		{"not a bulletin", "EA7KLK", false, 0, ""},
		{"wrong prefix", "BLX1NWS", false, 0, ""},
		{"no slot digit", "BLNNWS", false, 0, ""},
		{"too long overall", "BLN1NWSEXTRA", false, 0, ""},
		{"empty", "", false, 0, ""},
		{"addressed to a real user with BLN near", "BLN", false, 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			addr, ok := parseBulletinAddress(tc.to)
			if ok != tc.wantOK {
				t.Fatalf("parseBulletinAddress(%q) ok=%v, want %v", tc.to, ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if addr.Slot != tc.wantSl || addr.Group != tc.wantGrp {
				t.Fatalf("parseBulletinAddress(%q) = slot %d group %q, want %d %q",
					tc.to, addr.Slot, addr.Group, tc.wantSl, tc.wantGrp)
			}
		})
	}
}

func TestBulletinCategoryFromGroup(t *testing.T) {
	cases := map[string]string{
		"NWS":   "weather",
		"WX":    "weather",
		"SKYW":  "weather",
		"EMERG": "emergency",
		"ARES":  "emergency",
		"NTS":   "traffic",
		"NET":   "nets",
		"DX":    "dx",
		"SAT":   "satellite",
		"APRS":  "network",
		"TEST":  "test",
	}
	for group, want := range cases {
		if got := bulletinCategory(group, "irrelevant text"); got != want {
			t.Fatalf("bulletinCategory(%q) = %q, want %q", group, got, want)
		}
	}
}

func TestBulletinCategoryFallsBackToContent(t *testing.T) {
	// No group at all, so the text decides.
	if got := bulletinCategory("", "TORNADO WARNING until 21Z"); got != "weather" {
		t.Fatalf("content fallback = %q, want weather", got)
	}
	// An emergency word outranks a weather word when both appear.
	if got := bulletinCategory("", "FLOOD WARNING, EMERGENCY EVACUATION in progress"); got != "emergency" {
		t.Fatalf("precedence = %q, want emergency", got)
	}
	// Nothing recognisable and no group is an ordinary uncategorised bulletin.
	if got := bulletinCategory("", "Meeting moved to Thursday"); got != "general" {
		t.Fatalf("plain text = %q, want general", got)
	}
	// A group we do not know, with no recognisable content, is `other` rather
	// than being passed off as general.
	if got := bulletinCategory("XYZ", "Meeting moved to Thursday"); got != "other" {
		t.Fatalf("unknown group = %q, want other", got)
	}
	// An unknown group whose text is recognisable still categorises.
	if got := bulletinCategory("XYZ", "SEVERE THUNDERSTORM WATCH"); got != "weather" {
		t.Fatalf("unknown group with known content = %q, want weather", got)
	}
}

func TestBulletinCategoriesHaveLabels(t *testing.T) {
	a := testApp(t)
	// Load the real translations file, so a missing category label fails here
	// rather than showing a raw key to a user.
	raw := map[string]map[string]any{}
	if err := readJSON("../../translations.json", &raw, map[string]map[string]any{}); err != nil {
		t.Fatalf("reading translations: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("translations file not found from the test working directory")
	}
	a.text = raw
	for _, category := range bulletinCategories {
		for _, lang := range languageOrder {
			label := a.bulletinCategoryLabel(lang, category.ID)
			if label == "" || label == "aprs_bulletin_category_"+category.ID {
				t.Fatalf("no label for %q in %q", category.ID, lang)
			}
		}
	}
}

// The same bulletin heard twice updates one row; a different bulletin adds one.
func TestBulletinDedupeByContent(t *testing.T) {
	a := testApp(t)
	at := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)

	first := receivedAPRS{At: "2026-11-11 12:00 UTC", From: "NWS-OFFICE", To: "BLN1NWS",
		Text: "Tornado warning until 21:00Z", Raw: "NWS-OFFICE>APRS::BLN1NWS  :Tornado warning until 21:00Z"}
	id1, created, err := a.storeAPRSBulletin(first, bulletinAddress{Slot: 1, Group: "NWS"})
	if err != nil || !created {
		t.Fatalf("first store: created=%v err=%v", created, err)
	}

	// Same content, heard again later — different raw packet, same bulletin.
	repeat := receivedAPRS{At: "2026-11-11 12:10 UTC", From: "NWS-OFFICE", To: "BLN1NWS",
		Text: "Tornado warning until 21:00Z", Raw: "NWS-OFFICE>APRS::BLN1NWS  :Tornado warning until 21:00Z (repeat)"}
	id2, created, err := a.storeAPRSBulletin(repeat, bulletinAddress{Slot: 1, Group: "NWS"})
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("a repeat created a second row")
	}
	if id1 != id2 {
		t.Fatalf("repeat resolved to a different row: %d vs %d", id1, id2)
	}

	rows, err := a.loadAPRSBulletins("", at)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].SeenCount != 2 {
		t.Fatalf("SeenCount = %d, want 2", rows[0].SeenCount)
	}
	if rows[0].LastSeen != "2026-11-11 12:10 UTC" {
		t.Fatalf("LastSeen = %q, want the later timestamp", rows[0].LastSeen)
	}
	if rows[0].FirstSeen != "2026-11-11 12:00 UTC" {
		t.Fatalf("FirstSeen moved: %q", rows[0].FirstSeen)
	}

	// A genuinely different bulletin is a second row.
	other := receivedAPRS{At: "2026-11-11 12:20 UTC", From: "NWS-OFFICE", To: "BLN1NWS",
		Text: "Flood watch in effect", Raw: "NWS-OFFICE>APRS::BLN1NWS  :Flood watch in effect"}
	if _, created, err := a.storeAPRSBulletin(other, bulletinAddress{Slot: 1, Group: "NWS"}); err != nil || !created {
		t.Fatalf("second bulletin: created=%v err=%v", created, err)
	}
	rows, _ = a.loadAPRSBulletins("", at)
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
}

// Bulletin text is sanitised, like every other user-visible string.
func TestBulletinTextIsSanitised(t *testing.T) {
	a := testApp(t)
	msg := receivedAPRS{At: now(), From: "EA1ABC", To: "BLN1TEST",
		Text: "hello\x1b[2Jworld\x07", Raw: "EA1ABC>APRS::BLN1TEST  :hello\x1b[2Jworld"}
	if _, _, err := a.storeAPRSBulletin(msg, bulletinAddress{Slot: 1, Group: "TEST"}); err != nil {
		t.Fatal(err)
	}
	rows, err := a.loadAPRSBulletins("", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d", len(rows))
	}
	if strings.ContainsRune(rows[0].Text, '\x1b') || strings.ContainsRune(rows[0].Raw, '\x1b') {
		t.Fatalf("stored bulletin kept escape characters: %#v", rows[0])
	}
}

// Ten days is measured from last_seen, so a bulletin still being transmitted
// does not expire.
func TestBulletinRetentionUsesLastSeen(t *testing.T) {
	a := testApp(t)
	nowT := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)

	// First heard 20 days ago, last heard yesterday: still live.
	old := dbAPRSBulletin{
		DedupeKey: "live", FirstSeen: "2026-10-22 12:00 UTC", LastSeen: "2026-11-10 12:00 UTC",
		SeenCount: 40, From: "NWS", Slot: 1, Group: "NWS", Category: "weather", Text: "still repeating",
	}
	// Last heard 11 days ago: expired.
	stale := dbAPRSBulletin{
		DedupeKey: "stale", FirstSeen: "2026-10-20 12:00 UTC", LastSeen: "2026-10-31 12:00 UTC",
		SeenCount: 1, From: "NWS", Slot: 2, Group: "NWS", Category: "weather", Text: "gone",
	}
	for _, row := range []dbAPRSBulletin{old, stale} {
		r := row
		if err := a.db.Create(&r).Error; err != nil {
			t.Fatal(err)
		}
	}

	rows, err := a.loadAPRSBulletins("", nowT)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].DedupeKey != "live" {
		t.Fatalf("loadAPRSBulletins returned %#v", rows)
	}

	if err := a.pruneAPRSBulletins(nowT); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := a.db.Model(&dbAPRSBulletin{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("after prune: %d rows, want 1", count)
	}
}

func TestBulletinCounts(t *testing.T) {
	a := testApp(t)
	at := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	for _, row := range []dbAPRSBulletin{
		{DedupeKey: "a", LastSeen: "2026-11-11 11:00 UTC", Category: "weather", Text: "x"},
		{DedupeKey: "b", LastSeen: "2026-11-11 10:00 UTC", Category: "weather", Text: "y"},
		{DedupeKey: "c", LastSeen: "2026-11-11 09:00 UTC", Category: "nets", Text: "z"},
		{DedupeKey: "d", LastSeen: "2026-10-01 09:00 UTC", Category: "nets", Text: "old"},
	} {
		r := row
		if err := a.db.Create(&r).Error; err != nil {
			t.Fatal(err)
		}
	}
	counts, err := a.bulletinCounts(at)
	if err != nil {
		t.Fatal(err)
	}
	if counts["weather"] != 2 || counts["nets"] != 1 {
		t.Fatalf("counts = %#v, want weather:2 nets:1 (expired row excluded)", counts)
	}
}

func TestBulletinAge(t *testing.T) {
	at := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	cases := map[string]string{
		// Timestamps carry minute resolution, so "now" only applies to an exact
		// match; one minute earlier is 1m.
		"2026-11-11 12:00 UTC": "now",
		"2026-11-11 11:59 UTC": "1m",
		"2026-11-11 11:20 UTC": "40m",
		"2026-11-11 09:00 UTC": "3h",
		"2026-11-09 12:00 UTC": "2d",
		"not a date":           "",
	}
	for stamp, want := range cases {
		if got := bulletinAge(stamp, at); got != want {
			t.Fatalf("bulletinAge(%q) = %q, want %q", stamp, got, want)
		}
	}
}

// The screens: a category with bulletins lists them, and the detail shows what
// the design promises.
func TestBulletinScreens(t *testing.T) {
	a := testApp(t)
	installBasicText(a)
	a.text["en"]["aprs_bulletins_menu"] = "APRS bulletins"
	a.text["en"]["aprs_bulletin_title"] = "APRS bulletins"
	a.text["en"]["aprs_bulletin_detail"] = "APRS bulletin"
	a.text["en"]["aprs_bulletin_count"] = "bulletins"
	a.text["en"]["aprs_bulletin_window"] = "last 10 days"
	a.text["en"]["aprs_bulletin_group"] = "Group"
	a.text["en"]["aprs_bulletin_category"] = "Category"
	a.text["en"]["aprs_bulletin_slot"] = "Slot"
	a.text["en"]["aprs_bulletin_first_seen"] = "First heard"
	a.text["en"]["aprs_bulletin_last_seen"] = "Last heard"
	a.text["en"]["aprs_bulletin_times_heard"] = "Times heard"
	a.text["en"]["aprs_bulletin_none"] = "No bulletins"
	a.text["en"]["from"] = "From"
	a.text["en"]["at"] = "At"
	a.text["en"]["aprs_text"] = "Text"
	a.text["en"]["aprs_raw_packet"] = "Raw"
	a.text["en"]["back_button"] = "Back"
	a.text["en"]["aprs_bulletin_category_weather"] = "Weather alerts"
	a.text["en"]["aprs_bulletin_desc_weather"] = "Watches and warnings"

	at := now()
	msg := receivedAPRS{At: at, From: "NWS-OFFICE", To: "BLN1NWS",
		Text: "Tornado warning until 21:00Z", Raw: "NWS-OFFICE>APRS::BLN1NWS  :Tornado warning"}
	if _, _, err := a.storeAPRSBulletin(msg, bulletinAddress{Slot: 1, Group: "NWS"}); err != nil {
		t.Fatal(err)
	}

	var menus int
	var labels []string
	var detail [][]string
	a.runMenuHook = func(lang, title, header string, opts []option) string {
		menus++
		labels = labels[:0]
		for _, opt := range opts {
			labels = append(labels, opt.label)
		}
		switch menus {
		case 1:
			return "1" // the first (only) category
		case 2:
			return "1" // the first bulletin
		default:
			return "q"
		}
	}
	a.showInfoActionsHook = func(lang, title string, rows [][]string, actions []option) string {
		detail = rows
		return "q"
	}

	a.showAPRSBulletinCategories("en")

	if menus < 2 {
		t.Fatalf("only %d menus shown; the category and list screens did not run", menus)
	}
	if len(detail) == 0 {
		t.Fatal("the detail screen was never shown")
	}
	seen := map[string]string{}
	for _, row := range detail {
		if len(row) == 2 {
			seen[row[0]] = row[1]
		}
	}
	for _, want := range []string{"Group", "Category", "Times heard", "Text"} {
		if _, ok := seen[want]; !ok {
			t.Fatalf("detail missing %q: %#v", want, detail)
		}
	}
	if seen["Group"] != "NWS" {
		t.Fatalf("detail group = %q", seen["Group"])
	}
	if seen["Category"] != "Weather alerts" {
		t.Fatalf("detail category = %q, want the translated label", seen["Category"])
	}
}

// With nothing received, the screen explains rather than showing an empty menu.
func TestBulletinScreenEmptyState(t *testing.T) {
	a := testApp(t)
	a.text = map[string]map[string]any{"en": {
		"aprs_bulletin_title":     "APRS bulletins",
		"aprs_bulletin_none":      "No bulletins",
		"aprs_bulletin_none_hint": "They appear here",
		"back_button":             "Back",
	}}
	var shown [][]string
	a.showInfoActionsHook = func(lang, title string, rows [][]string, actions []option) string {
		shown = rows
		return "q"
	}
	a.showAPRSBulletinCategories("en")
	if len(shown) == 0 {
		t.Fatal("empty state not shown")
	}
}

// The row cap is a backstop for a flood inside one retention window.
func TestBulletinRowCap(t *testing.T) {
	a := testApp(t)
	at := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)

	// More rows than the cap, all inside the window so retention alone would
	// keep them.
	total := bulletinRowCap + 25
	err := a.db.Transaction(func(tx *gorm.DB) error {
		for i := 0; i < total; i++ {
			row := dbAPRSBulletin{
				DedupeKey: fmt.Sprintf("k%05d", i),
				LastSeen:  fmt.Sprintf("2026-11-%02d %02d:%02d UTC", 10, 0, i%60),
				FirstSeen: "2026-11-10 00:00 UTC",
				SeenCount: 1, From: "EA1ABC", Slot: 1, Group: "TEST",
				Category: "test", Text: fmt.Sprintf("bulletin %d", i),
			}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := a.pruneAPRSBulletins(at); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := a.db.Model(&dbAPRSBulletin{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if int(count) != bulletinRowCap {
		t.Fatalf("after cap prune: %d rows, want %d", count, bulletinRowCap)
	}
}

// A bulletin is never acknowledged: the receiver must route bulletins away from
// the per-user path entirely, or the ack queue would try to answer a group.
func TestBulletinIsNotTreatedAsUserMail(t *testing.T) {
	a := testApp(t)
	// A user whose callsign happens to look like nothing in particular.
	if err := a.saveUser("EA7KLK", userProfile{
		FullName: "Volker", Email: "v@example.org", Language: "en", EnableAPRS: true,
	}); err != nil {
		t.Fatal(err)
	}
	// A bulletin addressed to a group: this must not create a per-user row.
	if _, _, err := a.storeAPRSBulletin(
		receivedAPRS{At: now(), From: "NWS-OFFICE", To: "BLN1NWS", Text: "warning", Raw: "raw"},
		bulletinAddress{Slot: 1, Group: "NWS"}); err != nil {
		t.Fatal(err)
	}
	var received int64
	if err := a.db.Model(&dbAPRSReceived{}).Count(&received).Error; err != nil {
		t.Fatal(err)
	}
	if received != 0 {
		t.Fatalf("a bulletin created %d per-user rows", received)
	}
}

// The parse path, fed a real packet line, classifies and stores a bulletin.
func TestBulletinFromWirePacket(t *testing.T) {
	a := testApp(t)
	raw := "NWS-OFFICE>APRS,TCPIP*::BLN2NWS  :Tornado WARNING until 2100Z{ABC12"
	packet, ok := parseAPRSMessagePacket(raw)
	if !ok {
		t.Fatalf("packet did not parse: %q", raw)
	}
	addr, isBulletin := parseBulletinAddress(packet.Message.To)
	if !isBulletin {
		t.Fatalf("not recognised as a bulletin: %q", packet.Message.To)
	}
	if addr.Slot != 2 || addr.Group != "NWS" {
		t.Fatalf("address = %d/%q", addr.Slot, addr.Group)
	}
	if _, created, err := a.storeAPRSBulletin(packet.Message, addr); err != nil || !created {
		t.Fatalf("store: created=%v err=%v", created, err)
	}
	rows, err := a.loadAPRSBulletins("weather", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("weather bulletins = %d, want 1", len(rows))
	}
	if strings.Contains(rows[0].Text, "{ABC12") {
		t.Fatalf("message ID not stripped: %q", rows[0].Text)
	}
	if rows[0].From != "NWS-OFFICE" || rows[0].Slot != 2 {
		t.Fatalf("stored row = %#v", rows[0])
	}
}
