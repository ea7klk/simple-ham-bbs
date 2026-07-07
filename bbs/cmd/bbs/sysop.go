package main

import (
	"strconv"
	"strings"
)

func (a *app) sysopMenu(callsign, lang string) {
	for {
		users, _ := a.loadUsers()
		choice := a.runMenu(lang, a.t(lang, "sysop_menu_title"), "", []option{
			{"1", a.t(lang, "sysop_list_users")},
			{"2", a.t(lang, "sysop_toggle_sysop")},
			{"3", a.t(lang, "sysop_toggle_disabled")},
			{"4", a.t(lang, "sysop_delete_user")},
			{"5", a.t(lang, "sysop_publish_bulletin")},
			{"6", a.t(lang, "sysop_edit_bulletin")},
			{"7", a.t(lang, "sysop_add_board")},
			{"8", a.t(lang, "sysop_delete_board")},
			{"9", a.t(lang, "sysop_rename_board")},
			{"10", a.t(lang, "sysop_delete_message")},
			{"q", a.t(lang, "menu_quit")},
		})
		switch choice {
		case "1":
			rows := [][]string{}
			for _, key := range sortedKeys(users) {
				p := users[key]
				role := a.t(lang, "user_role")
				if a.isSysop(key, p) {
					role = a.t(lang, "sysop_role")
				}
				status := a.t(lang, "enabled")
				if p.Disabled {
					status = a.t(lang, "disabled")
				}
				rows = append(rows, []string{key, status + " / " + role + " / " + p.FullName + " / " + p.Email})
			}
			a.showInfo(lang, a.t(lang, "sysop_list_users"), rows)
		case "2":
			a.toggleSysop(callsign, lang, users)
		case "3":
			a.toggleDisabled(callsign, lang, users)
		case "4":
			a.deleteUser(callsign, lang, users)
		case "5":
			a.publishBulletin(callsign, lang)
		case "6":
			a.editBulletin(callsign, lang)
		case "7":
			a.addBoard(lang)
		case "8":
			a.deleteBoard(lang)
		case "9":
			a.renameBoard(lang)
		case "10":
			a.editBoardMessage(lang)
		case "q":
			return
		}
	}
}

func (a *app) chooseUser(lang string, users map[string]userProfile, current string) (string, bool) {
	opts := []option{}
	i := 1
	keys := sortedKeys(users)
	for _, key := range keys {
		if key == current {
			continue
		}
		opts = append(opts, option{strconv.Itoa(i), key + " - " + users[key].FullName})
		i++
	}
	opts = append(opts, option{"q", a.t(lang, "menu_quit")})
	choice := a.runMenu(lang, a.t(lang, "target_callsign"), "", opts)
	if choice == "q" {
		return "", false
	}
	idx, _ := strconv.Atoi(choice)
	if idx < 1 || idx > len(opts)-1 {
		return "", false
	}
	return strings.SplitN(opts[idx-1].label, " - ", 2)[0], true
}

func (a *app) toggleSysop(current, lang string, users map[string]userProfile) {
	target, ok := a.chooseUser(lang, users, current)
	if !ok {
		return
	}
	p := users[target]
	if a.cfg.sysops[target] {
		a.showInfo(lang, a.t(lang, "cannot_demote_configured"), [][]string{{target}})
		return
	}
	if a.isSysop(target, p) && a.wouldRemoveLastSysop(users, target) {
		a.showInfo(lang, a.t(lang, "cannot_remove_last_sysop"), [][]string{{target}})
		return
	}
	p.IsSysop = !a.isSysop(target, p)
	if p.IsSysop {
		p.Disabled = false
	}
	users[target] = p
	_ = a.saveUsers(users)
}

func (a *app) toggleDisabled(current, lang string, users map[string]userProfile) {
	target, ok := a.chooseUser(lang, users, current)
	if !ok {
		return
	}
	p := users[target]
	if !p.Disabled && a.wouldRemoveLastSysop(users, target) {
		a.showInfo(lang, a.t(lang, "cannot_remove_last_sysop"), [][]string{{target}})
		return
	}
	p.Disabled = !p.Disabled
	users[target] = p
	_ = a.saveUsers(users)
}

func (a *app) deleteUser(current, lang string, users map[string]userProfile) {
	target, ok := a.chooseUser(lang, users, current)
	if !ok {
		return
	}
	if a.wouldRemoveLastSysop(users, target) {
		a.showInfo(lang, a.t(lang, "cannot_remove_last_sysop"), [][]string{{target}})
		return
	}
	delete(users, target)
	_ = a.saveUsers(users)
}

func (a *app) addBoard(lang string) {
	data, _ := a.loadBoards()
	_, values, ok := a.runForm(lang, a.t(lang, "board_form_title"), []formField{{name: "name", label: a.t(lang, "board_name"), required: true, limit: 60}, {name: "description", label: a.t(lang, "board_description"), limit: 120}}, []string{"save", "cancel"})
	if !ok {
		return
	}
	id := boardID(values["name"])
	for _, b := range data.Boards {
		if b.ID == id {
			a.showInfo(lang, a.t(lang, "board_exists"), [][]string{{values["name"]}})
			return
		}
	}
	data.Boards = append(data.Boards, board{ID: id, Name: values["name"], Description: values["description"], Created: now()})
	_ = a.saveBoards(data)
}

func (a *app) deleteBoard(lang string) {
	data, _ := a.loadBoards()
	idx, ok := a.selectBoard(lang, data, "select_board_delete")
	if !ok || len(data.Boards) <= 1 {
		return
	}
	data.Boards = append(data.Boards[:idx], data.Boards[idx+1:]...)
	_ = a.saveBoards(data)
}

func (a *app) renameBoard(lang string) {
	data, _ := a.loadBoards()
	idx, ok := a.selectBoard(lang, data, "select_board_rename")
	if !ok {
		return
	}
	_, values, ok := a.runForm(lang, a.t(lang, "board_rename_title"), []formField{{name: "name", label: a.t(lang, "board_name"), value: data.Boards[idx].Name, required: true, limit: 60}}, []string{"save", "cancel"})
	if !ok {
		return
	}
	id := boardID(values["name"])
	for i, b := range data.Boards {
		if i != idx && b.ID == id {
			a.showInfo(lang, a.t(lang, "board_exists"), [][]string{{values["name"]}})
			return
		}
	}
	data.Boards[idx].Name, data.Boards[idx].ID = values["name"], id
	_ = a.saveBoards(data)
}

func (a *app) editBoardMessage(lang string) {
	data, _ := a.loadBoards()
	idx, ok := a.selectBoard(lang, data, "select_board_message_delete")
	if !ok || len(data.Boards[idx].Messages) == 0 {
		return
	}
	items := flattenMessages(data.Boards[idx].Messages)
	if len(items) == 0 {
		return
	}
	opts := []option{}
	for i, item := range items {
		opts = append(opts, option{strconv.Itoa(i + 1), messageMenuLabel(item)})
	}
	opts = append(opts, option{"q", a.t(lang, "menu_quit")})
	choice := a.runMenu(lang, a.t(lang, "select_message_delete"), "", opts)
	if choice == "q" {
		return
	}
	itemIdx, _ := strconv.Atoi(choice)
	itemIdx--
	if itemIdx < 0 || itemIdx >= len(items) {
		return
	}
	path := items[itemIdx].path
	msg := messageAtPath(data.Boards[idx].Messages, path)
	if msg == nil {
		return
	}
	action, values, ok := a.runForm(lang, a.t(lang, "message_edit_title"), []formField{{name: "subject", label: a.t(lang, "subject"), value: msg.Subject, required: true, limit: 80}, {name: "body", label: a.t(lang, "message_body"), value: msg.Body, kind: fieldTextArea, required: true, limit: 4000}}, []string{"cancel", "save", "delete"})
	if !ok && action != "delete" {
		return
	}
	if action == "delete" {
		if messages, deleted := deleteMessageAtPath(data.Boards[idx].Messages, path); deleted {
			data.Boards[idx].Messages = messages
		}
	} else {
		msg.Subject, msg.Body, msg.Edited = values["subject"], values["body"], now()
	}
	_ = a.saveBoards(data)
}
