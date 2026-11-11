package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// APRS bulletin screens: categories, then a list within a category, then detail.
//
// Read-only, and available to any logged-in user: `EnableAPRS` governs sending
// and receiving mail for an account, and reading public bulletins harms nobody.

// bulletinCategoryLabel translates a category id, falling back to the id itself
// so a row written by a newer version still displays something.
func (a *app) bulletinCategoryLabel(lang, category string) string {
	key := "aprs_bulletin_category_" + category
	if label := a.t(lang, key); label != key {
		return label
	}
	return category
}

func (a *app) bulletinCategoryDescription(lang, category string) string {
	key := "aprs_bulletin_desc_" + category
	if desc := a.t(lang, key); desc != key {
		return desc
	}
	return ""
}

// showAPRSBulletinCategories is the entry point from the APRS menu.
func (a *app) showAPRSBulletinCategories(lang string) {
	for {
		counts, err := a.bulletinCounts(time.Now())
		if err != nil {
			a.showMutationError(lang, a.t(lang, "aprs_bulletin_title"), err)
			return
		}
		total := 0
		for _, n := range counts {
			total += n
		}

		opts := []option{}
		order := []string{}
		// Only offer a category that has something in it, so the menu reflects
		// what has actually been received rather than the whole taxonomy.
		for _, category := range bulletinCategories {
			if counts[category.ID] == 0 {
				continue
			}
			order = append(order, category.ID)
			label := fmt.Sprintf("%-28s %4d", a.bulletinCategoryLabel(lang, category.ID), counts[category.ID])
			opts = append(opts, option{strconv.Itoa(len(order)), label})
		}
		if len(opts) == 0 {
			a.showInfoActions(lang, a.t(lang, "aprs_bulletin_title"), [][]string{
				{a.t(lang, "aprs_bulletin_none")},
				{a.t(lang, "aprs_bulletin_none_hint")},
			}, []option{{"q", a.t(lang, "back_button")}})
			return
		}
		opts = append(opts, option{"q", a.t(lang, "back_button")})
		header := fmt.Sprintf("%d %s / %s", total, a.t(lang, "aprs_bulletin_count"), a.t(lang, "aprs_bulletin_window"))
		choice := a.runMenu(lang, a.t(lang, "aprs_bulletin_title"), header, opts)
		if choice == "q" {
			return
		}
		idx, _ := strconv.Atoi(choice)
		if idx < 1 || idx > len(order) {
			continue
		}
		a.showAPRSBulletinsInCategory(lang, order[idx-1])
	}
}

// showAPRSBulletinsInCategory lists one category's bulletins, newest first.
func (a *app) showAPRSBulletinsInCategory(lang, category string) {
	for {
		rows, err := a.loadAPRSBulletins(category, time.Now())
		if err != nil {
			a.showMutationError(lang, a.bulletinCategoryLabel(lang, category), err)
			return
		}
		title := fmt.Sprintf("%s (%d)", a.bulletinCategoryLabel(lang, category), len(rows))
		if len(rows) == 0 {
			a.showInfoActions(lang, title, [][]string{{a.t(lang, "aprs_bulletin_none")}},
				[]option{{"q", a.t(lang, "back_button")}})
			return
		}
		opts := []option{}
		for i, item := range rows {
			opts = append(opts, option{strconv.Itoa(i + 1), a.bulletinListLabel(item, time.Now())})
		}
		opts = append(opts, option{"q", a.t(lang, "back_button")})
		header := a.bulletinCategoryDescription(lang, category)
		choice := a.runMenu(lang, title, header, opts)
		if choice == "q" {
			return
		}
		idx, _ := strconv.Atoi(choice)
		idx--
		if idx < 0 || idx >= len(rows) {
			continue
		}
		a.showAPRSBulletinDetail(lang, rows[idx])
	}
}

// bulletinListLabel renders one row: age, sender, then a one-line summary.
func (a *app) bulletinListLabel(item dbAPRSBulletin, at time.Time) string {
	const ageWidth = 5
	const fromWidth = 11
	summaryWidth := aprsListLabelWidth - ageWidth - fromWidth - 2
	if summaryWidth < 20 {
		summaryWidth = 20
	}
	summary := singleLineAPRSDetail(item.Text)
	group := item.Group
	if group == "" {
		group = "-"
	}
	return paddedCell(bulletinAge(item.LastSeen, at), ageWidth) +
		strings.Repeat(" ", 1) +
		paddedCell(item.From, fromWidth) +
		strings.Repeat(" ", 1) +
		paddedCell(group+" "+summary, summaryWidth)
}

func (a *app) showAPRSBulletinDetail(lang string, item dbAPRSBulletin) {
	group := item.Group
	if group == "" {
		group = a.t(lang, "aprs_bulletin_no_group")
	}
	rows := [][]string{
		{a.t(lang, "from"), item.From},
		{a.t(lang, "aprs_bulletin_group"), group},
		{a.t(lang, "aprs_bulletin_category"), a.bulletinCategoryLabel(lang, item.Category)},
		{a.t(lang, "aprs_bulletin_slot"), fmt.Sprintf("%d", item.Slot)},
		{a.t(lang, "aprs_bulletin_first_seen"), item.FirstSeen},
		{a.t(lang, "aprs_bulletin_last_seen"), item.LastSeen},
		{a.t(lang, "aprs_bulletin_times_heard"), fmt.Sprintf("%d", item.SeenCount)},
		{a.t(lang, "aprs_text"), singleLineAPRSDetail(item.Text)},
	}
	if item.Raw != "" {
		rows = append(rows,
			[]string{""},
			[]string{strings.Repeat("-", panelContentWidth)},
			[]string{""},
			[]string{a.t(lang, "aprs_raw_packet"), singleLineAPRSDetail(item.Raw)},
		)
	}
	a.showInfoActions(lang, a.t(lang, "aprs_bulletin_detail"), rows, []option{{"q", a.t(lang, "back_button")}})
}
