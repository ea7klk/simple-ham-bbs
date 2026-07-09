package main

import (
	"fmt"
	"gorm.io/gorm"
	"strconv"
)

func (a *app) loadBulletins() ([]bulletin, error) {
	rows := []dbBulletin{}
	err := a.db.Order("position, id").Find(&rows).Error
	out := make([]bulletin, 0, len(rows))
	for _, row := range rows {
		out = append(out, bulletin{Title: row.Title, Body: row.Body, Updated: row.Updated, From: row.From})
	}
	return out, err
}

func (a *app) saveBulletins(bulletins []bulletin) error {
	return a.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("DELETE FROM db_bulletins").Error; err != nil {
			return err
		}
		for i, item := range bulletins {
			row := dbBulletin{Position: i, Title: item.Title, Body: item.Body, Updated: item.Updated, From: item.From}
			if row.Updated == "" {
				row.Updated = now()
			}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
		}
		return nil
	})
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
	bulletins, _ := a.loadBulletins()
	bulletins = append(bulletins, bulletin{Title: values["title"], Body: values["body"], Updated: now(), From: callsign})
	_ = a.saveBulletins(bulletins)
	a.logBBSAction(callsign, "bulletin_create", "title=%q", values["title"])
	a.showInfo(lang, a.t(lang, "bulletin_published"), [][]string{{values["title"]}})
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
		bulletins = append(bulletins[:idx], bulletins[idx+1:]...)
		_ = a.saveBulletins(bulletins)
		a.logBBSAction(callsign, "bulletin_delete", "title=%q", title)
		a.showInfo(lang, a.t(lang, "bulletin_deleted"), [][]string{{title}})
		return
	}
	item.Title = values["title"]
	item.Body = values["body"]
	item.Updated = now()
	item.From = callsign
	bulletins[idx] = item
	_ = a.saveBulletins(bulletins)
	a.logBBSAction(callsign, "bulletin_update", "title=%q", item.Title)
	a.showInfo(lang, a.t(lang, "bulletin_updated"), [][]string{{item.Title}})
}
