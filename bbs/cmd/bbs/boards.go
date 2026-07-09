package main

import (
	"fmt"
	"gorm.io/gorm"
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
			Name:        row.Name,
			Description: row.Description,
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

func (a *app) saveBoards(data boardsData) error {
	return a.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("DELETE FROM db_messages").Error; err != nil {
			return err
		}
		if err := tx.Exec("DELETE FROM db_boards").Error; err != nil {
			return err
		}
		for i, b := range data.Boards {
			id := boardID(firstNonEmpty(b.ID, b.Name).(string))
			if id == "" {
				id = defaultBoardID
			}
			row := dbBoard{ID: id, Position: i, Name: clip(firstNonEmpty(b.Name, id).(string), 60), Description: clip(b.Description, 120), Created: firstNonEmpty(b.Created, now()).(string)}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
			if err := saveMessageRows(tx, id, nil, b.Messages); err != nil {
				return err
			}
		}
		return nil
	})
}

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
				From:    row.From,
				Subject: row.Subject,
				Body:    row.Body,
				Created: row.Created,
				Edited:  row.Edited,
				Replies: build(row.ID),
			})
		}
		return out
	}
	return build(0)
}

func saveMessageRows(tx *gorm.DB, boardID string, parentID *uint, messages []message) error {
	for i, msg := range messages {
		row := dbMessage{BoardID: boardID, ParentID: parentID, Position: i, From: msg.From, Subject: msg.Subject, Body: msg.Body, Created: msg.Created, Edited: msg.Edited}
		if row.Created == "" {
			row.Created = now()
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
		id := row.ID
		if err := saveMessageRows(tx, boardID, &id, msg.Replies); err != nil {
			return err
		}
	}
	return nil
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

func (a *app) postMessageToBoard(callsign, lang string, data *boardsData, boardIdx int) bool {
	if data == nil || boardIdx < 0 || boardIdx >= len(data.Boards) {
		return false
	}
	action, values, ok := a.runForm(lang, a.t(lang, "message_form_title")+" - "+data.Boards[boardIdx].Name, []formField{
		{name: "subject", label: a.t(lang, "subject"), required: true, limit: 80},
		{name: "body", label: a.t(lang, "message_body"), kind: fieldTextArea, required: true, limit: 4000},
	}, []string{"save", "cancel"})
	if !ok || action == "cancel" {
		return false
	}
	data.Boards[boardIdx].Messages = append(data.Boards[boardIdx].Messages, message{From: callsign, Subject: values["subject"], Body: values["body"], Created: now()})
	if len(data.Boards[boardIdx].Messages) > 500 {
		data.Boards[boardIdx].Messages = data.Boards[boardIdx].Messages[len(data.Boards[boardIdx].Messages)-500:]
	}
	_ = a.saveBoards(*data)
	a.logBBSAction(callsign, "message_post", "board=%q subject=%q", data.Boards[boardIdx].Name, values["subject"])
	a.showInfo(lang, a.t(lang, "message_posted"), [][]string{{values["subject"]}})
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

func (a *app) replyToMessage(callsign, lang string, data *boardsData, boardIdx int, path []int, parent message) bool {
	action, values, ok := a.runForm(lang, a.t(lang, "message_reply_title"), []formField{
		{name: "subject", label: a.t(lang, "subject"), value: replySubject(parent.Subject), required: true, limit: 80},
		{name: "body", label: a.t(lang, "message_body"), kind: fieldTextArea, required: true, limit: 4000},
	}, []string{"reply", "cancel"})
	if !ok || action == "cancel" {
		return false
	}
	reply := message{From: callsign, Subject: values["subject"], Body: values["body"], Created: now()}
	if !appendReplyAtPath(data.Boards[boardIdx].Messages, path, reply) {
		return false
	}
	_ = a.saveBoards(*data)
	a.logBBSAction(callsign, "message_reply", "board=%q subject=%q", data.Boards[boardIdx].Name, values["subject"])
	a.showInfo(lang, a.t(lang, "message_reply_posted"), [][]string{{values["subject"]}})
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

func appendReplyAtPath(messages []message, path []int, reply message) bool {
	msg := messageAtPath(messages, path)
	if msg == nil {
		return false
	}
	msg.Replies = append(msg.Replies, reply)
	return true
}

func deleteMessageAtPath(messages []message, path []int) ([]message, bool) {
	if len(path) == 0 {
		return messages, false
	}
	idx := path[0]
	if idx < 0 || idx >= len(messages) {
		return messages, false
	}
	if len(path) == 1 {
		return append(messages[:idx], messages[idx+1:]...), true
	}
	child, ok := deleteMessageAtPath(messages[idx].Replies, path[1:])
	if !ok {
		return messages, false
	}
	messages[idx].Replies = child
	return messages, true
}

func totalMessages(messages []message) int {
	total := 0
	for _, msg := range messages {
		total++
		total += totalMessages(msg.Replies)
	}
	return total
}
