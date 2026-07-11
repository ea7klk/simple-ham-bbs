package main

import (
	"strings"
	"testing"
)

func TestBulletinPersistenceRoundTrip(t *testing.T) {
	a := testApp(t)
	items := []bulletin{
		{Title: "Net", Body: "Weekly net", From: "EA7KLK"},
		{Title: "Repeater", Body: "Maintenance", Updated: "2026-07-10 12:00 UTC", From: "SYSOP"},
	}
	if err := a.saveBulletins(items); err != nil {
		t.Fatal(err)
	}
	loaded, err := a.loadBulletins()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 2 || loaded[0].Title != "Net" || loaded[0].Updated == "" || loaded[1].Updated != items[1].Updated {
		t.Fatalf("loadBulletins() = %#v", loaded)
	}
	if err := a.saveBulletins([]bulletin{{Title: "Only", Body: "One"}}); err != nil {
		t.Fatal(err)
	}
	loaded, _ = a.loadBulletins()
	if len(loaded) != 1 || loaded[0].Title != "Only" {
		t.Fatalf("saveBulletins replacement = %#v", loaded)
	}
}

func TestBoardsPersistenceAndThreadHelpers(t *testing.T) {
	a := testApp(t)
	data := boardsData{Boards: []board{
		{
			Name:        "APRS Traffic Board With A Very Long Name That Will Be Clipped By Storage",
			Description: strings.Repeat("description ", 20),
			Messages: []message{
				{
					From:    "EA7KLK",
					Subject: "Root",
					Body:    "Body",
					Replies: []message{
						{From: "EA1ABC", Subject: "Child", Body: "Reply"},
					},
				},
			},
		},
		{ID: "second", Name: "Second", Description: "Another"},
	}}
	if err := a.saveBoards(data); err != nil {
		t.Fatal(err)
	}
	loaded, err := a.loadBoards()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Boards) != 2 {
		t.Fatalf("loadBoards count = %d", len(loaded.Boards))
	}
	if loaded.Boards[0].ID != "aprs-traffic-board-with-a-very-long-name" || len(loaded.Boards[0].Description) != 120 {
		t.Fatalf("board clipping/id unexpected: %#v", loaded.Boards[0])
	}
	if got := totalMessages(loaded.Boards[0].Messages); got != 2 {
		t.Fatalf("totalMessages = %d, want 2", got)
	}
	items := flattenMessages(loaded.Boards[0].Messages)
	if len(items) != 2 || items[1].depth != 1 || items[1].path[0] != 0 || items[1].path[1] != 0 {
		t.Fatalf("flattenMessages() = %#v", items)
	}
	if label := messageMenuLabel(items[1]); !strings.Contains(label, "- Child") || !strings.Contains(label, "EA1ABC") {
		t.Fatalf("messageMenuLabel() = %q", label)
	}
	if msg := messageAtPath(loaded.Boards[0].Messages, []int{0, 0}); msg == nil || msg.Subject != "Child" {
		t.Fatalf("messageAtPath child = %#v", msg)
	}
	if msg := messageAtPath(loaded.Boards[0].Messages, []int{9}); msg != nil {
		t.Fatalf("messageAtPath invalid = %#v", msg)
	}
	if !appendReplyAtPath(loaded.Boards[0].Messages, []int{0, 0}, message{From: "EA2XYZ", Subject: "Grandchild"}) {
		t.Fatal("appendReplyAtPath failed")
	}
	if got := totalMessages(loaded.Boards[0].Messages); got != 3 {
		t.Fatalf("total after append = %d, want 3", got)
	}
	afterDelete, ok := deleteMessageAtPath(loaded.Boards[0].Messages, []int{0, 0, 0})
	if !ok || totalMessages(afterDelete) != 2 {
		t.Fatalf("delete grandchild ok=%v messages=%#v", ok, afterDelete)
	}
	afterDelete, ok = deleteMessageAtPath(afterDelete, []int{0})
	if !ok || len(afterDelete) != 0 {
		t.Fatalf("delete root ok=%v messages=%#v", ok, afterDelete)
	}
	if _, ok := deleteMessageAtPath(afterDelete, []int{0}); ok {
		t.Fatal("deleteMessageAtPath succeeded on missing item")
	}
}

func TestLoadBoardsDefaultWhenDatabaseEmpty(t *testing.T) {
	a := testApp(t)
	loaded, err := a.loadBoards()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Boards) != 1 || loaded.Boards[0].Name != "General" {
		t.Fatalf("empty loadBoards default = %#v", loaded)
	}
}
