package main

import (
	"fmt"
	"strconv"
	"strings"
)

func (a *app) loadBoards() (boardsData, error) {
	boardRows := []dbBoard{}
	if err := a.db.Order("position, id").Find(&boardRows).Error; err != nil {
		return boardsData{}, err
	}
	data := boardsData{}
	for _, row := range boardRows {
		data.Boards = append(data.Boards, board{
			ID:          row.ID,
			Name:        sanitizeSingleLineText(row.Name),
			Description: sanitizeSingleLineText(row.Description),
			Created:     row.Created,
			Messages:    a.loadBoardMessages(row.ID),
		})
	}
	if len(data.Boards) == 0 {
		data.Boards = []board{defaultBoard(nil)}
	}
	return data, nil
}

func defaultBoard(messages []message) board {
	return board{ID: defaultBoardID, Name: "General", Description: "General local messages", Created: now(), Messages: messages}
}

// loadBoardMessages builds the read-only thread view. Messages carry their row
// IDs so callers can address a reply or an edit without re-reading the board.
func (a *app) loadBoardMessages(boardID string) []message {
	rows := []dbMessage{}
	if err := a.db.Where("board_id = ?", boardID).Order("parent_id, position, id").Find(&rows).Error; err != nil {
		return nil
	}
	byParent := map[uint][]dbMessage{}
	for _, row := range rows {
		parent := uint(0)
		if row.ParentID != nil {
			parent = *row.ParentID
		}
		byParent[parent] = append(byParent[parent], row)
	}
	var build func(uint) []message
	build = func(parent uint) []message {
		out := []message{}
		for _, row := range byParent[parent] {
			out = append(out, message{
				ID:      row.ID,
				From:    sanitizeSingleLineText(row.From),
				Subject: sanitizeSingleLineText(row.Subject),
				Body:    sanitizeMultilineText(row.Body),
				Created: row.Created,
				Edited:  sanitizeSingleLineText(row.Edited),
				Replies: build(row.ID),
			})
		}
		return out
	}
	return build(0)
}

type threadedMessage struct {
	path  []int
	depth int
	msg   message
}

func (a *app) showMessages(callsign, lang string) {
	data, _ := a.loadBoards()
	boardIdx, ok := a.selectBoard(lang, data, "select_board")
	if !ok {
		return
	}
	for {
		data, _ = a.loadBoards()
		if boardIdx < 0 || boardIdx >= len(data.Boards) {
			return
		}
		b := data.Boards[boardIdx]
		items := flattenMessages(b.Messages)
		opts := []option{}
		for i, item := range items {
			opts = append(opts, option{strconv.Itoa(i + 1), messageMenuLabel(item)})
		}
		opts = append(opts, option{"p", a.t(lang, "menu_post")})
		opts = append(opts, option{"q", a.t(lang, "back_button")})
		header := ""
		if len(items) == 0 {
			header = a.t(lang, "no_messages")
		}
		choice := a.runMenu(lang, b.Name, header, opts)
		if choice == "q" {
			return
		}
		if choice == "p" {
			a.postMessageToBoard(callsign, lang, &data, boardIdx)
			continue
		}
		idx, _ := strconv.Atoi(choice)
		if idx < 1 || idx > len(items) {
			continue
		}
		if !a.readMessage(callsign, lang, &data, boardIdx, items[idx-1].path) {
			return
		}
	}
}

func (a *app) postMessage(callsign, lang string) {
	data, _ := a.loadBoards()
	boardIdx, ok := a.selectBoard(lang, data, "select_board_post")
	if !ok {
		return
	}
	a.postMessageToBoard(callsign, lang, &data, boardIdx)
}

// postMessageToBoard appends one row. The board the user is looking at is
// addressed by ID, so a concurrent edit to another board cannot touch it.
func (a *app) postMessageToBoard(callsign, lang string, data *boardsData, boardIdx int) bool {
	if data == nil || boardIdx < 0 || boardIdx >= len(data.Boards) {
		return false
	}
	boardKey := data.Boards[boardIdx].ID
	action, values, ok := a.runForm(lang, a.t(lang, "message_form_title")+" - "+data.Boards[boardIdx].Name, []formField{
		{name: "subject", label: a.t(lang, "subject"), required: true, limit: 80},
		{name: "body", label: a.t(lang, "message_body"), kind: fieldTextArea, required: true, limit: 4000},
	}, []string{"save", "cancel"})
	if !ok || action == "cancel" {
		return false
	}
	subject := sanitizeSingleLineText(values["subject"])
	body := sanitizeMultilineText(values["body"])
	if subject == "" || body == "" {
		a.showInfo(lang, a.t(lang, "message_form_title"), [][]string{{errorStyle.Render(a.t(lang, "required"))}})
		return false
	}
	if _, err := a.createMessage(boardKey, nil, message{From: callsign, Subject: subject, Body: body, Created: now()}); err != nil {
		a.showMutationError(lang, a.t(lang, "message_form_title"), err)
		return false
	}
	if err := a.trimBoardMessages(boardKey, 500); err != nil {
		a.showMutationError(lang, a.t(lang, "message_form_title"), err)
		return false
	}
	a.logBBSAction(callsign, "message_post", "board=%q subject=%q", data.Boards[boardIdx].Name, subject)
	a.showInfo(lang, a.t(lang, "message_posted"), [][]string{{subject}})
	return true
}

func (a *app) readMessage(callsign, lang string, data *boardsData, boardIdx int, path []int) bool {
	msg := messageAtPath(data.Boards[boardIdx].Messages, path)
	if msg == nil {
		return true
	}
	for {
		action := a.showInfoActions(lang, msg.Subject, [][]string{{a.t(lang, "message_board"), data.Boards[boardIdx].Name}, {a.t(lang, "from"), msg.From}, {a.t(lang, "at"), msg.Created}, {"Text", msg.Body}}, []option{{"r", a.t(lang, "reply_button")}, {"q", a.t(lang, "back_button")}})
		if action != "r" {
			return true
		}
		if a.replyToMessage(callsign, lang, data, boardIdx, path, *msg) {
			return true
		}
	}
}

// replyToMessage inserts one row under the parent's row ID. The parent is
// resolved from the thread view the user chose, and the insert names it
// directly, so a concurrent post to the same board cannot renumber it.
func (a *app) replyToMessage(callsign, lang string, data *boardsData, boardIdx int, path []int, parent message) bool {
	parentMsg := messageAtPath(data.Boards[boardIdx].Messages, path)
	if parentMsg == nil || parentMsg.ID == 0 {
		a.showInfo(lang, a.t(lang, "message_reply_title"), [][]string{{errorStyle.Render(a.t(lang, "required"))}})
		return false
	}
	action, values, ok := a.runForm(lang, a.t(lang, "message_reply_title"), []formField{
		{name: "subject", label: a.t(lang, "subject"), value: replySubject(parent.Subject), required: true, limit: 80},
		{name: "body", label: a.t(lang, "message_body"), kind: fieldTextArea, required: true, limit: 4000},
	}, []string{"reply", "cancel"})
	if !ok || action == "cancel" {
		return false
	}
	subject := sanitizeSingleLineText(values["subject"])
	body := sanitizeMultilineText(values["body"])
	if subject == "" || body == "" {
		a.showInfo(lang, a.t(lang, "message_reply_title"), [][]string{{errorStyle.Render(a.t(lang, "required"))}})
		return false
	}
	parentID := parentMsg.ID
	if _, err := a.createMessage(data.Boards[boardIdx].ID, &parentID, message{From: callsign, Subject: subject, Body: body, Created: now()}); err != nil {
		a.showMutationError(lang, a.t(lang, "message_reply_title"), err)
		return false
	}
	a.logBBSAction(callsign, "message_reply", "board=%q subject=%q", data.Boards[boardIdx].Name, subject)
	a.showInfo(lang, a.t(lang, "message_reply_posted"), [][]string{{subject}})
	return true
}

func (a *app) selectBoard(lang string, data boardsData, promptKey string) (int, bool) {
	if len(data.Boards) == 0 {
		a.showInfo(lang, a.t(lang, "message_boards_title"), [][]string{{a.t(lang, "no_boards")}})
		return 0, false
	}
	opts := []option{}
	for i, b := range data.Boards {
		opts = append(opts, option{strconv.Itoa(i + 1), fmt.Sprintf("%s (%d) - %s", b.Name, totalMessages(b.Messages), b.Description)})
	}
	opts = append(opts, option{"q", a.t(lang, "menu_quit")})
	choice := a.runMenu(lang, a.t(lang, promptKey), "", opts)
	if choice == "q" {
		return 0, false
	}
	idx, _ := strconv.Atoi(choice)
	return idx - 1, idx >= 1 && idx <= len(data.Boards)
}

func flattenMessages(messages []message) []threadedMessage {
	items := []threadedMessage{}
	var walk func([]message, []int, int)
	walk = func(list []message, base []int, depth int) {
		for i, msg := range list {
			path := append(append([]int{}, base...), i)
			items = append(items, threadedMessage{path: path, depth: depth, msg: msg})
			walk(msg.Replies, path, depth+1)
		}
	}
	walk(messages, nil, 0)
	return items
}

func messageMenuLabel(item threadedMessage) string {
	prefix := strings.Repeat("  ", item.depth)
	if item.depth > 0 {
		prefix += "- "
	}
	return fmt.Sprintf("%s%s - %s - %s", prefix, item.msg.Subject, item.msg.From, item.msg.Created)
}

func messageAtPath(messages []message, path []int) *message {
	if len(path) == 0 || path[0] < 0 || path[0] >= len(messages) {
		return nil
	}
	msg := &messages[path[0]]
	for _, idx := range path[1:] {
		if idx < 0 || idx >= len(msg.Replies) {
			return nil
		}
		msg = &msg.Replies[idx]
	}
	return msg
}

func totalMessages(messages []message) int {
	total := 0
	for _, msg := range messages {
		total++
		total += totalMessages(msg.Replies)
	}
	return total
}
