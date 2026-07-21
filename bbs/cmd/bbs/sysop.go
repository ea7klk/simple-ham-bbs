package main

import (
	"fmt"
	"github.com/charmbracelet/lipgloss"
	"gorm.io/gorm"
	"strconv"
	"strings"
)

func (a *app) sysopMenu(callsign, lang string) {
	for {
		users, _ := a.loadUsers()
		choice := a.runMenu(lang, a.t(lang, "sysop_menu_title"), "", []option{
			{"1", a.t(lang, "sysop_list_users")},
			{"2", a.t(lang, "sysop_toggle_sysop")},
			{"3", a.t(lang, "sysop_manage_bulletins")},
			{"4", a.t(lang, "sysop_add_board")},
			{"5", a.t(lang, "sysop_delete_board")},
			{"6", a.t(lang, "sysop_rename_board")},
			{"7", a.t(lang, "sysop_delete_message")},
			{"q", a.t(lang, "menu_quit")},
		})
		switch choice {
		case "1":
			a.listUsers(callsign, lang, users)
		case "2":
			a.toggleSysop(callsign, lang, users)
		case "3":
			a.manageBulletins(callsign, lang)
		case "4":
			a.addBoard(callsign, lang)
		case "5":
			a.deleteBoard(callsign, lang)
		case "6":
			a.renameBoard(callsign, lang)
		case "7":
			a.editBoardMessage(callsign, lang)
		case "q":
			return
		}
	}
}

func (a *app) listUsers(current, lang string, users map[string]userProfile) {
	for {
		target, ok := a.chooseUser(lang, users, "")
		if !ok {
			return
		}
		a.editUserDetail(current, lang, users, target)
		refreshed, err := a.loadUsers()
		if err != nil {
			return
		}
		users = refreshed
	}
}

func (a *app) chooseUser(lang string, users map[string]userProfile, current string) (string, bool) {
	opts := []option{}
	targets := []string{}
	i := 1
	keys := sortedKeys(users)
	const callsignWidth = 10
	const lastSeenWidth = 20
	const statusWidth = 10
	const roleWidth = 8
	nameWidth := panelContentWidth - 7 - callsignWidth - lastSeenWidth - statusWidth - roleWidth - 4
	if nameWidth < 12 {
		nameWidth = 12
	}
	for _, key := range keys {
		if key == current {
			continue
		}
		p := users[key]
		role := a.t(lang, "user_role")
		if a.isSysop(key, p) {
			role = a.t(lang, "sysop_role")
		}
		status := a.t(lang, "enabled")
		if p.Disabled {
			status = a.t(lang, "disabled")
		}
		row := userListRow(key, p.LastSeen, status, role, p.FullName, nameWidth)
		opts = append(opts, option{strconv.Itoa(i), row})
		targets = append(targets, key)
		i++
	}
	opts = append(opts, option{"q", a.t(lang, "menu_quit")})
	header := userListRow(a.t(lang, "target_callsign"), a.t(lang, "last_connection"), a.t(lang, "account_status"), a.t(lang, "role"), a.t(lang, "full_name"), nameWidth)
	choice := a.runMenu(lang, a.t(lang, "target_callsign"), header, opts)
	if choice == "q" {
		return "", false
	}
	idx, _ := strconv.Atoi(choice)
	if idx < 1 || idx > len(opts)-1 {
		return "", false
	}
	return targets[idx-1], true
}

func userListRow(callsign, lastSeen, status, role, fullName string, nameWidth int) string {
	if lastSeen == "" {
		lastSeen = "-"
	}
	return paddedCell(callsign, 10) + " " +
		paddedCell(lastSeen, 20) + " " +
		paddedCell(status, 10) + " " +
		paddedCell(role, 8) + " " +
		truncateText(fullName, nameWidth)
}

func paddedCell(text string, width int) string {
	text = truncateText(text, width)
	padding := width - lipgloss.Width(text)
	if padding < 0 {
		padding = 0
	}
	return text + strings.Repeat(" ", padding)
}

func (a *app) editUserDetail(current, lang string, users map[string]userProfile, target string) {
	profile, ok := users[target]
	if !ok {
		a.showInfo(lang, a.t(lang, "user_not_found"), [][]string{{target}})
		return
	}
	action, values, ok := a.runForm(lang, a.t(lang, "user_detail_title")+" - "+target, a.userDetailFields(lang, profile), []string{"cancel", "save", "delete"})
	if action == "delete" {
		a.confirmAndDeleteUser(current, lang, users, target)
		return
	}
	if !ok || action == "cancel" {
		return
	}
	updated := applyProfileValues(profile, values)
	updated.Disabled = values["account_status"] == "disabled"
	if target == current && !profile.Disabled && updated.Disabled {
		a.showInfo(lang, a.t(lang, "cannot_manage_self"), [][]string{{target}})
		return
	}
	if updated.Disabled && a.wouldRemoveLastSysopWithProfile(users, target, updated) {
		a.showInfo(lang, a.t(lang, "cannot_remove_last_sysop"), [][]string{{target}})
		return
	}
	users[target] = updated
	_ = a.saveUsers(users)
	a.logBBSAction(current, "user_update", "target=%q disabled=%t", target, updated.Disabled)
	a.showInfo(lang, a.t(lang, "user_updated"), userDetailRows(a, lang, target, updated, a.isSysop(target, updated)))
}

func (a *app) userDetailFields(lang string, profile userProfile) []formField {
	profileLang := profile.Language
	if profileLang == "" {
		profileLang = "en"
	}
	status := "enabled"
	if profile.Disabled {
		status = "disabled"
	}
	return []formField{
		{name: "full_name", label: a.t(lang, "full_name"), value: profile.FullName, required: true, limit: 100},
		{name: "email", label: a.t(lang, "email"), value: profile.Email, required: true, limit: 120, validator: func(v string) bool { return emailRE.MatchString(v) }, invalidText: a.t(lang, "invalid_email")},
		{name: "maidenhead", label: a.t(lang, "maidenhead"), value: profile.Maidenhead, limit: 10, normalizer: normalizeLocator, validator: func(v string) bool { return v == "" || maidenheadRE.MatchString(v) }, invalidText: a.t(lang, "invalid_locator")},
		{name: "language", label: a.t(lang, "language"), kind: fieldChoice, value: profileLang, required: true, choices: languageOptions()},
		{name: "enable_aprs", label: a.t(lang, "enable_aprs"), kind: fieldChoice, value: boolString(profile.EnableAPRS), choices: []option{{"false", "false"}, {"true", "true"}}},
		{name: "account_status", label: a.t(lang, "account_status"), kind: fieldChoice, value: status, choices: []option{{"enabled", a.t(lang, "enabled")}, {"disabled", a.t(lang, "disabled")}}},
		{name: "qth", label: a.t(lang, "qth"), value: profile.QTH, limit: 80},
		{name: "rig", label: a.t(lang, "rig"), value: profile.Rig, limit: 100},
	}
}

func userDetailRows(a *app, lang, callsign string, profile userProfile, isSysop bool) [][]string {
	role := a.t(lang, "user_role")
	if isSysop {
		role = a.t(lang, "sysop_role")
	}
	status := a.t(lang, "enabled")
	if profile.Disabled {
		status = a.t(lang, "disabled")
	}
	rows := [][]string{{a.t(lang, "target_callsign"), callsign}, {a.t(lang, "account_status"), status}, {a.t(lang, "sysop"), role}}
	rows = append(rows, profileRows(a, lang, profile)...)
	return rows
}

func (a *app) confirmAndDeleteUser(current, lang string, users map[string]userProfile, target string) {
	if target == current {
		a.showInfo(lang, a.t(lang, "cannot_manage_self"), [][]string{{target}})
		return
	}
	if a.wouldRemoveLastSysop(users, target) {
		a.showInfo(lang, a.t(lang, "cannot_remove_last_sysop"), [][]string{{target}})
		return
	}
	if !a.confirmDelete(lang, fmt.Sprintf(a.t(lang, "confirm_delete_user"), target)) {
		return
	}
	delete(users, target)
	_ = a.deleteUserAPRSHistory(target)
	_ = a.saveUsers(users)
	a.logBBSAction(current, "user_delete", "target=%q", target)
	a.showInfo(lang, a.t(lang, "user_deleted"), [][]string{{target}})
}

func (a *app) deleteUserAPRSHistory(callsign string) error {
	key := normalizeCallsign(callsign)
	return a.db.Transaction(func(tx *gorm.DB) error {
		var sentRows []dbAPRSSent
		if err := tx.Where("user_callsign = ?", key).Find(&sentRows).Error; err != nil {
			return err
		}
		for _, row := range sentRows {
			if err := tx.Delete(&dbAPRSSentPart{}, "sent_id = ?", row.ID).Error; err != nil {
				return err
			}
		}
		if err := tx.Delete(&dbAPRSSent{}, "user_callsign = ?", key).Error; err != nil {
			return err
		}
		return tx.Delete(&dbAPRSReceived{}, "user_callsign = ?", key).Error
	})
}

func (a *app) wouldRemoveLastSysopWithProfile(users map[string]userProfile, callsign string, profile userProfile) bool {
	current, ok := users[callsign]
	if !ok || !a.isSysop(callsign, current) || current.Disabled {
		return false
	}
	if a.isSysop(callsign, profile) && !profile.Disabled {
		return false
	}
	count := 0
	for key, value := range users {
		if key == callsign {
			continue
		}
		if a.isSysop(key, value) && !value.Disabled {
			count++
		}
	}
	return count == 0
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
	a.logBBSAction(current, "sysop_toggle", "target=%q enabled=%t", target, p.IsSysop)
}

func (a *app) addBoard(callsign, lang string) {
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
	a.logBBSAction(callsign, "board_create", "board=%q", values["name"])
}

func (a *app) deleteBoard(callsign, lang string) {
	data, _ := a.loadBoards()
	idx, ok := a.selectBoard(lang, data, "select_board_delete")
	if !ok {
		return
	}
	if len(data.Boards) <= 1 {
		a.showInfo(lang, a.t(lang, "sysop_delete_board"), [][]string{{a.t(lang, "cannot_delete_last_board")}})
		return
	}
	if !a.confirmDelete(lang, fmt.Sprintf(a.t(lang, "confirm_delete_board"), data.Boards[idx].Name)) {
		return
	}
	name := data.Boards[idx].Name
	data.Boards = append(data.Boards[:idx], data.Boards[idx+1:]...)
	_ = a.saveBoards(data)
	a.logBBSAction(callsign, "board_delete", "board=%q", name)
}

func (a *app) renameBoard(callsign, lang string) {
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
	oldName := data.Boards[idx].Name
	data.Boards[idx].Name, data.Boards[idx].ID = values["name"], id
	_ = a.saveBoards(data)
	a.logBBSAction(callsign, "board_rename", "from=%q to=%q", oldName, values["name"])
}

func (a *app) editBoardMessage(callsign, lang string) {
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
	logAction := ""
	logDetail := ""
	if action == "delete" {
		if !a.confirmDelete(lang, fmt.Sprintf(a.t(lang, "confirm_delete_message"), msg.Subject)) {
			return
		}
		if messages, deleted := deleteMessageAtPath(data.Boards[idx].Messages, path); deleted {
			data.Boards[idx].Messages = messages
			logAction = "message_delete"
			logDetail = fmt.Sprintf("board=%q subject=%q", data.Boards[idx].Name, msg.Subject)
		}
	} else {
		msg.Subject, msg.Body, msg.Edited = values["subject"], values["body"], now()
		logAction = "message_edit"
		logDetail = fmt.Sprintf("board=%q subject=%q", data.Boards[idx].Name, msg.Subject)
	}
	_ = a.saveBoards(data)
	if logAction != "" {
		a.logBBSAction(callsign, logAction, logDetail)
	}
}
