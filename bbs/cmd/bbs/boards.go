package main

import (
	"fmt"
	"strconv"
	"strings"
)

func (a *app) loadBoards() (boardsData, error) {
	var raw any
	if !exists(a.cfg.messagesFile) {
		data := boardsData{Boards: []board{defaultBoard(nil)}}
		return data, writeJSON(a.cfg.messagesFile, data)
	}
	if err := readJSON(a.cfg.messagesFile, &raw, nil); err != nil {
		return boardsData{}, err
	}
	data := normalizeBoards(raw)
	_ = writeJSON(a.cfg.messagesFile, data)
	return data, nil
}

func normalizeBoards(raw any) boardsData {
	switch v := raw.(type) {
	case []any:
		msgs := []message{}
		for _, item := range v {
			if m, ok := mapToMessage(item); ok {
				msgs = append(msgs, m)
			}
		}
		return boardsData{Boards: []board{defaultBoard(msgs)}}
	case map[string]any:
		items, _ := v["boards"].([]any)
		out := boardsData{}
		seen := map[string]bool{}
		for _, item := range items {
			bm, ok := item.(map[string]any)
			if !ok {
				continue
			}
			name := strings.TrimSpace(fmt.Sprint(firstNonEmpty(bm["name"], bm["id"], "General")))
			id := boardID(fmt.Sprint(firstNonEmpty(bm["id"], name)))
			base := id
			for i := 2; seen[id]; i++ {
				id = fmt.Sprintf("%s-%d", base, i)
			}
			seen[id] = true
			msgs := []message{}
			if rawMsgs, ok := bm["messages"].([]any); ok {
				for _, rawMsg := range rawMsgs {
					if m, ok := mapToMessage(rawMsg); ok {
						msgs = append(msgs, m)
					}
				}
			}
			out.Boards = append(out.Boards, board{ID: id, Name: clip(name, 60), Description: clip(fmt.Sprint(bm["description"]), 120), Created: fmt.Sprint(firstNonEmpty(bm["created"], now())), Messages: msgs})
		}
		if len(out.Boards) > 0 {
			return out
		}
	}
	return boardsData{Boards: []board{defaultBoard(nil)}}
}

func defaultBoard(messages []message) board {
	return board{ID: defaultBoardID, Name: "General", Description: "General local messages", Created: now(), Messages: messages}
}

func (a *app) saveBoards(data boardsData) error {
	return writeJSON(a.cfg.messagesFile, data)
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
	b := data.Boards[boardIdx]
	if len(b.Messages) == 0 {
		a.showInfo(lang, b.Name, [][]string{{a.t(lang, "no_messages")}})
		return
	}
	for {
		data, _ = a.loadBoards()
		b = data.Boards[boardIdx]
		items := flattenMessages(b.Messages)
		if len(items) == 0 {
			a.showInfo(lang, b.Name, [][]string{{a.t(lang, "no_messages")}})
			return
		}
		opts := []option{}
		for i, item := range items {
			opts = append(opts, option{strconv.Itoa(i + 1), messageMenuLabel(item)})
		}
		opts = append(opts, option{"q", a.t(lang, "menu_quit")})
		choice := a.runMenu(lang, b.Name, "", opts)
		if choice == "q" {
			return
		}
		idx, _ := strconv.Atoi(choice)
		if idx < 1 || idx > len(items) {
			return
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
	action, values, ok := a.runForm(lang, a.t(lang, "message_form_title")+" - "+data.Boards[boardIdx].Name, []formField{
		{name: "subject", label: a.t(lang, "subject"), required: true, limit: 80},
		{name: "body", label: a.t(lang, "message_body"), kind: fieldTextArea, required: true, limit: 4000},
	}, []string{"save", "cancel"})
	if !ok || action == "cancel" {
		return
	}
	data.Boards[boardIdx].Messages = append(data.Boards[boardIdx].Messages, message{From: callsign, Subject: values["subject"], Body: values["body"], Created: now()})
	if len(data.Boards[boardIdx].Messages) > 500 {
		data.Boards[boardIdx].Messages = data.Boards[boardIdx].Messages[len(data.Boards[boardIdx].Messages)-500:]
	}
	_ = a.saveBoards(data)
	a.showInfo(lang, a.t(lang, "message_posted"), [][]string{{values["subject"]}})
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
