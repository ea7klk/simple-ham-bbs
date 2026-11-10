package main

import (
	"strings"
	"testing"
)

func TestSanitizeTerminalTextRemovesControlAndSpoofingCharacters(t *testing.T) {
	cases := []struct {
		name   string
		in     string
		single string
		multi  string
	}{
		{"csi clear screen", "hi\x1b[2Jthere", "hi[2Jthere", "hi[2Jthere"},
		{"osc 52 clipboard", "a\x1b]52;c;ZXZpbA==\x07b", "a]52;c;ZXZpbA==b", "a]52;c;ZXZpbA==b"},
		{"bare escape", "a\x1bb", "ab", "ab"},
		{"c1 csi", "a\u009b2Jb", "a2Jb", "a2Jb"},
		{"c1 control", "before \u009f after", "before after", "before  after"},
		{"del", "a\x7fb", "ab", "ab"},
		{"right-to-left override", "abc\u202edef", "abcdef", "abcdef"},
		{"isolate", "abc\u2067def\u2069", "abcdef", "abcdef"},
		{"zero width space", "a\u200bb", "ab", "ab"},
		{"byte order mark", "\ufeffa", "a", "a"},
		{"tab", "a\tb", "a b", "a b"},
		{"newline in single line", "a\nb", "a b", "a\nb"},
		{"crlf normalised", "a\r\nb", "a b", "a\nb"},
		{"lone cr normalised", "a\rb", "a b", "a\nb"},
		{"blank line run capped", "a\n\n\n\nb", "a b", "a\n\nb"},
		{"invalid utf8", "a\xffb", "a\uFFFDb", "a\uFFFDb"},
		{"accents kept", "Müller", "Müller", "Müller"},
		{"cjk kept", "日本語", "日本語", "日本語"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitizeSingleLineText(tc.in); got != tc.single {
				t.Fatalf("sanitizeSingleLineText(%q) = %q, want %q", tc.in, got, tc.single)
			}
			if got := sanitizeMultilineText(tc.in); got != tc.multi {
				t.Fatalf("sanitizeMultilineText(%q) = %q, want %q", tc.in, got, tc.multi)
			}
		})
	}
}

func TestSanitizeTerminalTextIsIdempotent(t *testing.T) {
	inputs := []string{"hi\x1b[2Jthere", "a\n\n\n\nb", "x\x1b]52;c;eA==\x07y", "abc\u202edef", "a\xffb"}
	for _, in := range inputs {
		once := sanitizeMultilineText(in)
		if twice := sanitizeMultilineText(once); twice != once {
			t.Fatalf("multiline not idempotent for %q: %q then %q", in, once, twice)
		}
		if twice := sanitizeSingleLineText(sanitizeSingleLineText(in)); twice != sanitizeSingleLineText(in) {
			t.Fatalf("single line not idempotent for %q", in)
		}
	}
}

func TestFormValidationStripsTerminalControlsButNotPasswords(t *testing.T) {
	a := testApp(t)
	m := newFormModel(a, "en", "Test", []formField{
		{name: "subject", label: "Subject", kind: fieldText},
		{name: "body", label: "Body", kind: fieldTextArea},
	}, []string{"save"})
	// Bubbles widgets already drop C0 controls from typed input, but they keep
	// bidi overrides and zero-width characters, so the payload includes both and
	// the shared rule is what removes the second kind.
	m.fields[0].input.SetValue("hi\x1b[2Jthere\u202e")
	m.fields[1].area.SetValue("line1\x1b]52;c;eA==\x07\n\n\n\nline2\u200b")

	values, errText := m.validate()
	if errText != "" {
		t.Fatalf("validate() error = %q", errText)
	}
	if values["subject"] != "hi[2Jthere" {
		t.Fatalf("subject = %q", values["subject"])
	}
	if values["body"] != "line1]52;c;eA==\n\nline2" {
		t.Fatalf("body = %q", values["body"])
	}

	// Passwords are never rendered and must stay byte-exact so verification keeps
	// working; the field rule deliberately leaves them alone.
	if got := sanitizeFieldText(fieldPassword, "pa\x1bss"); got != "pa\x1bss" {
		t.Fatalf("password field was rewritten: %q", got)
	}
}

func TestPostedMessageCannotCarryTerminalSequences(t *testing.T) {
	a := testApp(t)
	installBasicText(a)
	a.showInfoHook = func(lang, title string, rows [][]string) {}
	a.runFormHook = func(lang, title string, fields []formField, buttons []string) (string, map[string]string, bool) {
		return "save", map[string]string{
			"subject": "hi\x1b[2Jthere",
			"body":    "body\x1b]52;c;ZXZpbA==\x07\n\n\n\nend",
		}, true
	}

	data, _ := a.loadBoards()
	if !a.postMessageToBoard("EA7KLK", "en", &data, 0) {
		t.Fatal("postMessageToBoard() rejected a valid post")
	}

	boards, err := a.loadBoards()
	if err != nil {
		t.Fatal(err)
	}
	msg := boards.Boards[0].Messages[0]
	if msg.Subject != "hi[2Jthere" {
		t.Fatalf("stored subject = %q", msg.Subject)
	}
	if msg.Body != "body]52;c;ZXZpbA==\n\nend" {
		t.Fatalf("stored body = %q", msg.Body)
	}

	// The two render paths that used to pass escapes through unchanged.
	label := messageMenuLabel(threadedMessage{msg: msg})
	if strings.ContainsRune(label, '\x1b') {
		t.Fatalf("menu label still contains ESC: %q", label)
	}
	rendered := panelStyle.Render(strings.Join(wrapText(msg.Body, panelContentWidth), "\n"))
	if strings.ContainsRune(rendered, '\x1b') {
		t.Fatalf("rendered body still carries an escape: %q", rendered)
	}
}

func TestRepliesAndBulletinsStripTerminalSequences(t *testing.T) {
	a := testApp(t)
	installBasicText(a)
	a.showInfoHook = func(lang, title string, rows [][]string) {}

	data, _ := a.loadBoards()
	data.Boards[0].Messages = []message{{From: "EA1ABC", Subject: "Root", Body: "Body", Created: now()}}
	if err := a.replaceAllBoards(data); err != nil {
		t.Fatal(err)
	}

	a.runFormHook = func(lang, title string, fields []formField, buttons []string) (string, map[string]string, bool) {
		return "reply", map[string]string{"subject": "Re: \x1b[2JRoot", "body": "reply\x1b]8;;http://evil\x07"}, true
	}
	current, _ := a.loadBoards()
	if !a.replyToMessage("EA7KLK", "en", &current, 0, []int{0}, current.Boards[0].Messages[0]) {
		t.Fatal("replyToMessage() rejected a valid reply")
	}

	a.runFormHook = func(lang, title string, fields []formField, buttons []string) (string, map[string]string, bool) {
		return "save", map[string]string{"title": "Net \x1b[2Jnotes", "body": "text\x1b[31mred"}, true
	}
	a.addBulletin("EA7KLK", "en")

	boards, _ := a.loadBoards()
	reply := boards.Boards[0].Messages[0].Replies[0]
	if strings.ContainsRune(reply.Subject, '\x1b') || strings.ContainsRune(reply.Body, '\x1b') {
		t.Fatalf("reply kept escapes: %#v", reply)
	}
	bulletins, _ := a.loadBulletins()
	if len(bulletins) != 1 || strings.ContainsRune(bulletins[0].Title, '\x1b') || strings.ContainsRune(bulletins[0].Body, '\x1b') {
		t.Fatalf("bulletin kept escapes: %#v", bulletins)
	}
}

func TestBoardAndProfileTextStripsTerminalSequences(t *testing.T) {
	a := testApp(t)
	installBasicText(a)
	a.showInfoHook = func(lang, title string, rows [][]string) {}
	// Production always has the seeded default board; adding a board must not
	// disturb it.
	if err := a.createBoard(defaultBoard(nil)); err != nil {
		t.Fatal(err)
	}

	a.runFormHook = func(lang, title string, fields []formField, buttons []string) (string, map[string]string, bool) {
		return "save", map[string]string{"name": "APRS \x1b[2JTraffic", "description": "desc\x1b]52;c;eA==\x07"}, true
	}
	a.addBoard("EA7KLK", "en")
	boards, _ := a.loadBoards()
	if len(boards.Boards) != 2 {
		t.Fatalf("addBoard() boards = %#v", boards.Boards)
	}
	added := boards.Boards[1]
	if strings.ContainsRune(added.Name, '\x1b') || strings.ContainsRune(added.Description, '\x1b') {
		t.Fatalf("board kept escapes: %#v", added)
	}

	updated := applyProfileValues(userProfile{}, map[string]string{
		"full_name": "Volker \x1b[2J",
		"email":     "v@example.com",
		"qth":       "Home\x1b]52;c;eA==\x07",
		"rig":       "HT\u202e",
		"language":  "en",
	})
	if strings.ContainsRune(updated.FullName, '\x1b') || strings.ContainsRune(updated.QTH, '\x1b') || strings.ContainsRune(updated.Rig, '\u202e') {
		t.Fatalf("profile kept escapes: %#v", updated)
	}
	if updated.FullName != "Volker [2J" {
		t.Fatalf("profile full name = %q", updated.FullName)
	}
}

// Text written before this fix (or by any path that bypasses the write guards)
// must not survive a read: the loaders are the last line of defence.
func TestLegacyStoredTextIsCleanedOnLoad(t *testing.T) {
	a := testApp(t)
	payload := "hi\x1b[2Jthere"
	if err := a.db.Create(&dbBoard{ID: "general", Position: 0, Name: "Gen\x1beral", Description: "d\x1b[2J"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := a.db.Create(&dbMessage{BoardID: "general", From: "EA7KLK", Subject: payload, Body: payload + "\n\n\n\nx", Created: now()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := a.db.Create(&dbBulletin{Position: 0, Title: payload, Body: payload, Updated: now()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := a.db.Create(&dbUser{Callsign: "EA7KLK", FullName: "Volker \x1b[2J", Email: "v@example.com", QTH: "Home\x1b]52;c;eA==\x07", Rig: "HT\u202e", Language: "en"}).Error; err != nil {
		t.Fatal(err)
	}

	boards, err := a.loadBoards()
	if err != nil {
		t.Fatal(err)
	}
	msg := boards.Boards[0].Messages[0]
	checks := map[string]string{
		"board name":        boards.Boards[0].Name,
		"board description": boards.Boards[0].Description,
		"message from":      msg.From,
		"message subject":   msg.Subject,
		"message body":      msg.Body,
	}
	bulletins, _ := a.loadBulletins()
	if len(bulletins) != 1 {
		t.Fatalf("loadBulletins() = %#v", bulletins)
	}
	checks["bulletin title"] = bulletins[0].Title
	checks["bulletin body"] = bulletins[0].Body
	users, _ := a.loadUsers()
	profile := users["EA7KLK"]
	checks["user full name"] = profile.FullName
	checks["user qth"] = profile.QTH
	checks["user rig"] = profile.Rig

	for name, value := range checks {
		if strings.ContainsRune(value, '\x1b') || strings.ContainsRune(value, '\u202e') {
			t.Fatalf("%s still contains a terminal control: %q", name, value)
		}
	}
	if msg.Subject != "hi[2Jthere" || msg.Body != "hi[2Jthere\n\nx" {
		t.Fatalf("loaded message = %#v", msg)
	}
}

// A field that is only escapes must not be stored as blank content.
func TestEscapeOnlyFieldsAreRejected(t *testing.T) {
	a := testApp(t)
	installBasicText(a)
	rejected := false
	a.showInfoHook = func(lang, title string, rows [][]string) { rejected = true }
	a.runFormHook = func(lang, title string, fields []formField, buttons []string) (string, map[string]string, bool) {
		return "save", map[string]string{"subject": "\x1b[2J\x1b]52;c;eA==\x07", "body": "\x1b\x07"}, true
	}
	data, _ := a.loadBoards()
	if a.postMessageToBoard("EA7KLK", "en", &data, 0) {
		t.Fatal("postMessageToBoard() stored a message made only of escape sequences")
	}
	if !rejected {
		t.Fatal("postMessageToBoard() did not tell the user the fields were empty")
	}
	boards, _ := a.loadBoards()
	if totalMessages(boards.Boards[0].Messages) != 0 {
		t.Fatalf("board gained a message: %#v", boards.Boards[0].Messages)
	}
}
