package main

import (
	"errors"
	"fmt"
	"testing"
)

// seedThread builds a chain of replies and returns the IDs from root to the
// deepest message. The chain may not exceed maxReplyDepth, so callers stop one
// level earlier to test the refusal.
func seedThread(t *testing.T, a *app, depth int) []uint {
	t.Helper()
	if err := a.createBoard(board{ID: defaultBoardID, Name: "General"}); err != nil {
		t.Fatal(err)
	}
	root, err := a.createMessage(defaultBoardID, nil, message{From: "EA1ABC", Subject: "root", Body: "x"})
	if err != nil {
		t.Fatal(err)
	}
	ids := []uint{root}
	parent := root
	for i := 0; i < depth; i++ {
		id, err := a.createMessage(defaultBoardID, &parent, message{From: "EA1ABC", Subject: fmt.Sprintf("reply-%d", i+1), Body: "x"})
		if err != nil {
			t.Fatalf("reply %d at depth %d: %v", i+1, i+1, err)
		}
		ids = append(ids, id)
		parent = id
	}
	return ids
}

func TestReplyDepthAllowsUpToLimit(t *testing.T) {
	a := testApp(t)
	ids := seedThread(t, a, maxReplyDepth)
	if len(ids) != maxReplyDepth+1 {
		t.Fatalf("thread seeded with %d rows", len(ids))
	}

	boards, err := a.loadBoards()
	if err != nil {
		t.Fatal(err)
	}
	items := flattenMessages(boards.Boards[0].Messages)
	if len(items) != maxReplyDepth+1 {
		t.Fatalf("loaded %d messages", len(items))
	}
	for i, item := range items {
		if item.msg.Depth != i {
			t.Fatalf("message %d has depth %d", i, item.msg.Depth)
		}
	}
}

func TestReplyDepthRejectsOneLevelTooDeep(t *testing.T) {
	a := testApp(t)
	ids := seedThread(t, a, maxReplyDepth)
	deepest := ids[len(ids)-1]

	if _, err := a.createMessage(defaultBoardID, &deepest, message{From: "EA1ABC", Subject: "too-deep", Body: "x"}); !errors.Is(err, errThreadTooDeep) {
		t.Fatalf("createMessage() error = %v, want errThreadTooDeep", err)
	}

	boards, err := a.loadBoards()
	if err != nil {
		t.Fatal(err)
	}
	if got := totalMessages(boards.Boards[0].Messages); got != maxReplyDepth+1 {
		t.Fatalf("rejected reply was stored: %d messages", got)
	}
}

// The cap is enforced in the storage layer, so a caller that bypasses the form
// cannot exceed it either.
func TestReplyDepthIsEnforcedWithoutTheUI(t *testing.T) {
	a := testApp(t)
	ids := seedThread(t, a, maxReplyDepth)
	deepest := ids[len(ids)-1]

	for i := 0; i < 3; i++ {
		if _, err := a.createMessage(defaultBoardID, &deepest, message{From: "x", Subject: "y", Body: "z"}); !errors.Is(err, errThreadTooDeep) {
			t.Fatalf("attempt %d error = %v, want errThreadTooDeep", i, err)
		}
	}
}

// Replying through the UI shows a translated message instead of a raw error.
func TestReplyToMessageReportsDepthLimit(t *testing.T) {
	a := testApp(t)
	installBasicText(a)
	a.text["en"]["thread_too_deep"] = "Replies can be nested at most 5 levels deep."
	seedThread(t, a, maxReplyDepth)

	var shown [][]string
	a.showInfoHook = func(lang, title string, rows [][]string) { shown = rows }
	a.runFormHook = func(lang, title string, fields []formField, buttons []string) (string, map[string]string, bool) {
		return "reply", map[string]string{"subject": "Re: x", "body": "y"}, true
	}

	data, err := a.loadBoards()
	if err != nil {
		t.Fatal(err)
	}
	items := flattenMessages(data.Boards[0].Messages)
	path := items[len(items)-1].path
	parent := messageAtPath(data.Boards[0].Messages, path)
	if parent == nil {
		t.Fatal("deepest message not found")
	}
	if a.replyToMessage("EA7KLK", "en", &data, 0, path, *parent) {
		t.Fatal("replyToMessage() accepted a reply past the depth limit")
	}
	if len(shown) == 0 || len(shown[0]) == 0 || shown[0][0] == "" {
		t.Fatalf("no depth-limit message shown: %#v", shown)
	}
}

// Only top-level messages count toward the 500-message budget; a deep thread
// must not be trimmed away because of its replies.
func TestRepliesDoNotCountTowardMessageBudget(t *testing.T) {
	a := testApp(t)
	if err := a.createBoard(board{ID: defaultBoardID, Name: "General"}); err != nil {
		t.Fatal(err)
	}
	// Build 500 top-level messages, one of which carries a full-depth thread.
	var threaded uint
	for i := 0; i < 500; i++ {
		id, err := a.createMessage(defaultBoardID, nil, message{From: "EA1ABC", Subject: fmt.Sprintf("top-%d", i), Body: "x"})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			threaded = id
		}
	}
	parent := threaded
	for i := 0; i < maxReplyDepth; i++ {
		id, err := a.createMessage(defaultBoardID, &parent, message{From: "EA1ABC", Subject: fmt.Sprintf("reply-%d", i+1), Body: "x"})
		if err != nil {
			t.Fatal(err)
		}
		parent = id
	}

	before, err := a.loadBoards()
	if err != nil {
		t.Fatal(err)
	}
	// 500 top-level messages plus a 5-deep reply chain (6 rows below the root).
	if got := totalMessages(before.Boards[0].Messages); got != 500+maxReplyDepth {
		t.Fatalf("rows before trim = %d, want %d", got, 500+maxReplyDepth)
	}

	// The budget is on top-level messages, so this is a no-op.
	if err := a.trimBoardMessages(defaultBoardID, 500); err != nil {
		t.Fatal(err)
	}
	after, err := a.loadBoards()
	if err != nil {
		t.Fatal(err)
	}
	if got := totalMessages(after.Boards[0].Messages); got != 500+maxReplyDepth {
		t.Fatalf("trim counted replies against the budget: %d rows left", got)
	}

	// One more top-level message does trip the budget, and only the oldest
	// thread is dropped.
	if _, err := a.createMessage(defaultBoardID, nil, message{From: "EA1ABC", Subject: "top-500", Body: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := a.trimBoardMessages(defaultBoardID, 500); err != nil {
		t.Fatal(err)
	}
	trimmed, err := a.loadBoards()
	if err != nil {
		t.Fatal(err)
	}
	if got := totalMessages(trimmed.Boards[0].Messages); got != 500 {
		t.Fatalf("rows after trim = %d, want 500", got)
	}
	for _, msg := range trimmed.Boards[0].Messages {
		if msg.Subject == "top-0" {
			t.Fatal("oldest thread survived the trim")
		}
	}
}

func TestMessageDepthWalksParents(t *testing.T) {
	a := testApp(t)
	ids := seedThread(t, a, maxReplyDepth)
	for want, id := range ids {
		got, err := messageDepth(a.db, id)
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("messageDepth(%d) = %d, want %d", id, got, want)
		}
	}
}
