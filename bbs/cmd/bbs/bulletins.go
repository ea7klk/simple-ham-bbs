package main

import (
	"fmt"
	"strconv"
)

func (a *app) loadBulletins() ([]bulletin, error) {
	rows := []dbBulletin{}
	err := a.db.Order("position, id").Find(&rows).Error
	out := make([]bulletin, 0, len(rows))
	for _, row := range rows {
		out = append(out, bulletin{ID: row.ID, Title: sanitizeSingleLineText(row.Title), Body: sanitizeMultilineText(row.Body), Updated: row.Updated, From: sanitizeSingleLineText(row.From)})
	}
	return out, err
}

func (a *app) showBulletins(lang string) {
	bulletins, _ := a.loadBulletins()
	if len(bulletins) == 0 {
		a.showInfo(lang, a.t(lang, "menu_bulletins"), [][]string{{a.t(lang, "no_bulletins")}})
		return
	}
	opts := []option{}
	for i, item := range bulletins {
		opts = append(opts, option{strconv.Itoa(i + 1), item.Title + " - " + item.Updated})
	}
	opts = append(opts, option{"q", a.t(lang, "menu_quit")})
	choice := a.runMenu(lang, a.t(lang, "menu_bulletins"), "", opts)
	if choice == "q" {
		return
	}
	idx, _ := strconv.Atoi(choice)
	if idx < 1 || idx > len(bulletins) {
		return
	}
	item := bulletins[idx-1]
	a.showInfo(lang, item.Title, [][]string{{a.t(lang, "at"), item.Updated}, {a.t(lang, "from"), item.From}, {"Text", item.Body}})
}

func (a *app) manageBulletins(callsign, lang string) {
	for {
		bulletins, _ := a.loadBulletins()
		opts := []option{{"a", a.t(lang, "add_new_bulletin")}}
		for i, item := range bulletins {
			opts = append(opts, option{strconv.Itoa(i + 1), item.Title + " - " + item.Updated})
		}
		opts = append(opts, option{"q", a.t(lang, "menu_quit")})
		header := ""
		if len(bulletins) == 0 {
			header = a.t(lang, "no_bulletins")
		}
		choice := a.runMenu(lang, a.t(lang, "sysop_manage_bulletins"), header, opts)
		if choice == "q" {
			return
		}
		if choice == "a" {
			a.addBulletin(callsign, lang)
			continue
		}
		idx, _ := strconv.Atoi(choice)
		idx--
		if idx < 0 || idx >= len(bulletins) {
			continue
		}
		a.editBulletinAt(callsign, lang, bulletins, idx)
	}
}

func (a *app) addBulletin(callsign, lang string) {
	action, values, ok := a.runForm(lang, a.t(lang, "bulletin_form_title"), []formField{
		{name: "title", label: a.t(lang, "bulletin_title"), required: true, limit: 100},
		{name: "body", label: a.t(lang, "bulletin_body"), kind: fieldTextArea, required: true, limit: 4000},
	}, []string{"save", "cancel"})
	if !ok || action == "cancel" {
		return
	}
	title := sanitizeSingleLineText(values["title"])
	body := sanitizeMultilineText(values["body"])
	if title == "" || body == "" {
		a.showInfo(lang, a.t(lang, "bulletin_form_title"), [][]string{{errorStyle.Render(a.t(lang, "required"))}})
		return
	}
	if _, err := a.createBulletin(bulletin{Title: title, Body: body, Updated: now(), From: callsign}); err != nil {
		a.showMutationError(lang, a.t(lang, "bulletin_form_title"), err)
		return
	}
	a.logBBSAction(callsign, "bulletin_create", "title=%q", title)
	a.showInfo(lang, a.t(lang, "bulletin_published"), [][]string{{title}})
}

func (a *app) editBulletinAt(callsign, lang string, bulletins []bulletin, idx int) {
	item := bulletins[idx]
	action, values, ok := a.runForm(lang, a.t(lang, "bulletin_edit_title"), []formField{
		{name: "title", label: a.t(lang, "bulletin_title"), value: item.Title, required: true, limit: 100},
		{name: "body", label: a.t(lang, "bulletin_body"), value: item.Body, kind: fieldTextArea, required: true, limit: 4000},
	}, []string{"cancel", "save", "delete"})
	if !ok && action != "delete" {
		return
	}
	if action == "delete" {
		title := item.Title
		if !a.confirmDelete(lang, fmt.Sprintf(a.t(lang, "confirm_delete_bulletin"), title)) {
			return
		}
		if err := a.deleteBulletinRow(item.ID); err != nil {
			a.showMutationError(lang, a.t(lang, "bulletin_edit_title"), err)
			return
		}
		a.logBBSAction(callsign, "bulletin_delete", "title=%q", title)
		a.showInfo(lang, a.t(lang, "bulletin_deleted"), [][]string{{title}})
		return
	}
	title := sanitizeSingleLineText(values["title"])
	body := sanitizeMultilineText(values["body"])
	if title == "" || body == "" {
		a.showInfo(lang, a.t(lang, "bulletin_edit_title"), [][]string{{errorStyle.Render(a.t(lang, "required"))}})
		return
	}
	updated := now()
	if err := a.updateBulletinRow(item.ID, title, body, updated, callsign); err != nil {
		a.showMutationError(lang, a.t(lang, "bulletin_edit_title"), err)
		return
	}
	a.logBBSAction(callsign, "bulletin_update", "title=%q", title)
	a.showInfo(lang, a.t(lang, "bulletin_updated"), [][]string{{title}})
}
