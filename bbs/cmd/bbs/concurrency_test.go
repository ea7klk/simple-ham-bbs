package main

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

// Each SSH session is a separate bbs_app process with its own database handle,
// so these tests open independent handles on one file. That is the arrangement
// the old whole-table rewrite broke: two sessions read the same snapshot and the
// second save deleted the first session's rows.

func sessionPair(t *testing.T, a *app) *app {
	t.Helper()
	db, err := openDatabase(a.cfg.dbFile)
	if err != nil {
		t.Fatal(err)
	}
	second := *a
	second.db = db
	return &second
}

func TestConcurrentPostsDoNotLoseEachOthersMessages(t *testing.T) {
	first := testApp(t)
	if err := first.seedData(); err != nil {
		t.Fatal(err)
	}
	second := sessionPair(t, first)

	// Both sessions look at the board before either writes, reproducing the
	// interleaving that used to lose a post.
	firstView, _ := first.loadBoards()
	secondView, _ := second.loadBoards()
	if len(firstView.Boards) != 1 || len(secondView.Boards) != 1 {
		t.Fatal("expected one seeded board")
	}

	if _, err := first.createMessage(firstView.Boards[0].ID, nil, message{From: "ALICE", Subject: "from-alice", Body: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := second.createMessage(secondView.Boards[0].ID, nil, message{From: "BOB", Subject: "from-bob", Body: "y"}); err != nil {
		t.Fatal(err)
	}

	final, err := first.loadBoards()
	if err != nil {
		t.Fatal(err)
	}
	var subjects []string
	for _, msg := range final.Boards[0].Messages {
		subjects = append(subjects, msg.Subject)
	}
	if !contains(subjects, "from-alice") || !contains(subjects, "from-bob") {
		t.Fatalf("a concurrent post was lost: %v", subjects)
	}
}

func TestConcurrentWritesFromTwoSessionsAllLand(t *testing.T) {
	first := testApp(t)
	if err := first.seedData(); err != nil {
		t.Fatal(err)
	}
	second := sessionPair(t, first)
	boardID := defaultBoardID

	const perSession = 10
	var wg sync.WaitGroup
	errs := make(chan error, perSession*2)
	for _, session := range []*app{first, second} {
		wg.Add(1)
		go func(a *app) {
			defer wg.Done()
			for i := 0; i < perSession; i++ {
				if _, err := a.createMessage(boardID, nil, message{From: "EA7KLK", Subject: fmt.Sprintf("post-%d", i), Body: "x"}); err != nil {
					errs <- err
					return
				}
			}
		}(session)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent write failed: %v", err)
	}

	final, err := first.loadBoards()
	if err != nil {
		t.Fatal(err)
	}
	if got := totalMessages(final.Boards[0].Messages); got != perSession*2 {
		t.Fatalf("total messages = %d, want %d", got, perSession*2)
	}
}

func TestStaleSessionCannotDeleteAnotherSessionsAccount(t *testing.T) {
	first := testApp(t)
	second := sessionPair(t, first)

	// Session 1 reads the (empty) user table and keeps that snapshot, as the old
	// authenticate() did before saving.
	stale, _ := first.loadUsers()
	if len(stale) != 0 {
		t.Fatalf("expected an empty user table, got %v", sortedKeys(stale))
	}

	// Session 2 registers an account.
	if err := second.saveUser("NEWUSER", userProfile{FullName: "New", Email: "n@example.com", Language: "en", PasswordHash: "h"}); err != nil {
		t.Fatal(err)
	}

	// Session 1 now updates its own row only.
	if err := first.saveUser("OTHER", userProfile{FullName: "Other", Email: "o@example.com", Language: "en", PasswordHash: "h"}); err != nil {
		t.Fatal(err)
	}

	after, err := first.loadUsers()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := after["NEWUSER"]; !ok {
		t.Fatalf("concurrently registered account was deleted: %v", sortedKeys(after))
	}
	if _, ok := after["OTHER"]; !ok {
		t.Fatalf("own account missing: %v", sortedKeys(after))
	}
}

func TestConcurrentProfileSavesDoNotOverwriteEachOther(t *testing.T) {
	first := testApp(t)
	second := sessionPair(t, first)
	if err := first.saveUser("EA7KLK", userProfile{FullName: "Volker", Email: "v@example.com", Language: "en"}); err != nil {
		t.Fatal(err)
	}
	if err := first.saveUser("EA1ABC", userProfile{FullName: "Alice", Email: "a@example.com", Language: "en"}); err != nil {
		t.Fatal(err)
	}

	volker, _, _ := first.loadUser("EA7KLK")
	alice, _, _ := second.loadUser("EA1ABC")

	volker.QTH = "Home"
	if err := first.saveUser("EA7KLK", volker); err != nil {
		t.Fatal(err)
	}
	alice.Rig = "HT"
	if err := second.saveUser("EA1ABC", alice); err != nil {
		t.Fatal(err)
	}

	afterVolker, _, _ := first.loadUser("EA7KLK")
	afterAlice, _, _ := first.loadUser("EA1ABC")
	if afterVolker.QTH != "Home" {
		t.Fatalf("first session's profile change was lost: %#v", afterVolker)
	}
	if afterAlice.Rig != "HT" {
		t.Fatalf("second session's profile change was lost: %#v", afterAlice)
	}
}

func TestLoginDoesNotRewriteOtherAccounts(t *testing.T) {
	a := testApp(t)
	if err := a.replaceAllUsers(map[string]userProfile{
		"EA7KLK": {FullName: "Volker", Email: "v@example.com", Language: "en", IsSysop: true, LastSeen: "2026-01-01 00:00 UTC"},
		"EA1ABC": {FullName: "Alice", Email: "a@example.com", Language: "en", LastSeen: "2026-01-01 00:00 UTC"},
	}); err != nil {
		t.Fatal(err)
	}
	a.cfg.sysops = map[string]bool{"EA1ABC": true}

	users, err := a.loadUsers()
	if err != nil {
		t.Fatal(err)
	}
	for _, callsign := range a.applyConfiguredSysops(users) {
		if err := a.saveUser(callsign, users[callsign]); err != nil {
			t.Fatal(err)
		}
	}

	// A sysop already exists, so BBS_SYSOPS has no authority and must not be
	// applied. Nobody's row may be rewritten by a login either.
	alice, _, _ := a.loadUser("EA1ABC")
	if alice.IsSysop {
		t.Fatalf("configured callsign was promoted while a sysop already existed: %#v", alice)
	}
	volker, _, _ := a.loadUser("EA7KLK")
	if !volker.IsSysop || volker.LastSeen != "2026-01-01 00:00 UTC" {
		t.Fatalf("unrelated account changed: %#v", volker)
	}
}

func TestRenameBoardMovesItsMessages(t *testing.T) {
	a := testApp(t)
	if err := a.createBoard(board{ID: "general", Name: "General", Description: "General"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.createMessage("general", nil, message{From: "EA7KLK", Subject: "Kept", Body: "Body"}); err != nil {
		t.Fatal(err)
	}
	if err := a.renameBoardRow("general", boardID("APRS Traffic"), "APRS Traffic"); err != nil {
		t.Fatal(err)
	}

	boards, err := a.loadBoards()
	if err != nil {
		t.Fatal(err)
	}
	if len(boards.Boards) != 1 || boards.Boards[0].ID != "aprs-traffic" || boards.Boards[0].Name != "APRS Traffic" {
		t.Fatalf("rename result = %#v", boards.Boards)
	}
	if got := totalMessages(boards.Boards[0].Messages); got != 1 {
		t.Fatalf("messages after rename = %d, want 1", got)
	}
	if boards.Boards[0].Messages[0].Subject != "Kept" {
		t.Fatalf("message content changed: %#v", boards.Boards[0].Messages[0])
	}
}

func TestDeleteBoardLeavesOtherBoardsIntact(t *testing.T) {
	a := testApp(t)
	for _, b := range []board{{ID: "general", Name: "General"}, {ID: "aprs", Name: "APRS"}} {
		if err := a.createBoard(b); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.createMessage("general", nil, message{From: "EA7KLK", Subject: "general-post", Body: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.createMessage("aprs", nil, message{From: "EA7KLK", Subject: "aprs-post", Body: "y"}); err != nil {
		t.Fatal(err)
	}

	if err := a.deleteBoardRow("general"); err != nil {
		t.Fatal(err)
	}

	boards, err := a.loadBoards()
	if err != nil {
		t.Fatal(err)
	}
	if len(boards.Boards) != 1 || boards.Boards[0].ID != "aprs" {
		t.Fatalf("boards after delete = %#v", boards.Boards)
	}
	if got := totalMessages(boards.Boards[0].Messages); got != 1 || boards.Boards[0].Messages[0].Subject != "aprs-post" {
		t.Fatalf("surviving board corrupted: %#v", boards.Boards[0].Messages)
	}
}

func TestDeleteMessageSubtreeRemovesNestedReplies(t *testing.T) {
	a := testApp(t)
	if err := a.createBoard(board{ID: "general", Name: "General"}); err != nil {
		t.Fatal(err)
	}
	root, err := a.createMessage("general", nil, message{From: "EA7KLK", Subject: "root", Body: "x"})
	if err != nil {
		t.Fatal(err)
	}
	child, err := a.createMessage("general", &root, message{From: "EA1ABC", Subject: "child", Body: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.createMessage("general", &child, message{From: "EA2XYZ", Subject: "grandchild", Body: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.createMessage("general", nil, message{From: "EA3ABC", Subject: "sibling", Body: "x"}); err != nil {
		t.Fatal(err)
	}

	if err := a.deleteMessageSubtree("general", child); err != nil {
		t.Fatal(err)
	}

	boards, err := a.loadBoards()
	if err != nil {
		t.Fatal(err)
	}
	items := flattenMessages(boards.Boards[0].Messages)
	if len(items) != 2 {
		t.Fatalf("messages after subtree delete = %#v", items)
	}
	for _, item := range items {
		if item.msg.Subject == "child" || item.msg.Subject == "grandchild" {
			t.Fatalf("deleted subtree survived: %#v", items)
		}
	}
}

func TestTrimBoardMessagesKeepsTheNewestAndDropsTheirReplies(t *testing.T) {
	a := testApp(t)
	if err := a.createBoard(board{ID: "general", Name: "General"}); err != nil {
		t.Fatal(err)
	}
	var firstID uint
	for i := 0; i < 5; i++ {
		id, err := a.createMessage("general", nil, message{From: "EA7KLK", Subject: fmt.Sprintf("msg-%d", i), Body: "x"})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			firstID = id
		}
	}
	if _, err := a.createMessage("general", &firstID, message{From: "EA1ABC", Subject: "reply-to-oldest", Body: "x"}); err != nil {
		t.Fatal(err)
	}

	if err := a.trimBoardMessages("general", 3); err != nil {
		t.Fatal(err)
	}

	boards, err := a.loadBoards()
	if err != nil {
		t.Fatal(err)
	}
	items := flattenMessages(boards.Boards[0].Messages)
	if len(items) != 3 {
		t.Fatalf("trimmed thread count = %d, want 3: %#v", len(items), items)
	}
	for _, item := range items {
		if item.msg.Subject == "reply-to-oldest" {
			t.Fatal("reply to a trimmed parent survived as an orphan")
		}
	}
	if items[0].msg.Subject != "msg-2" || items[2].msg.Subject != "msg-4" {
		t.Fatalf("trim kept the wrong messages: %#v", items)
	}
}

func TestDatabaseOpensWithImmediateTransactions(t *testing.T) {
	a := testApp(t)
	var mode string
	if err := a.db.Raw("PRAGMA journal_mode").Scan(&mode).Error; err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}
	if _, err := openDatabase(filepath.Join(t.TempDir(), "bbs.sqlite")); err != nil {
		t.Fatal(err)
	}
}
