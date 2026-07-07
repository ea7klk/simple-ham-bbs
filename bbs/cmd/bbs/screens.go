package main

import "fmt"

func (a *app) stationDirectory(lang string) {
	users, _ := a.loadUsers()
	rows := [][]string{}
	keys := sortedKeys(users)
	for _, callsign := range keys {
		p := users[callsign]
		if p.Disabled {
			continue
		}
		rows = append(rows, []string{callsign, fmt.Sprintf("%s / %s / %s", p.FullName, p.Maidenhead, languages[p.Language])})
	}
	a.showInfo(lang, a.t(lang, "menu_directory"), rows)
}

func (a *app) radioResources(lang string) {
	a.showInfo(lang, a.t(lang, "resources_title"), [][]string{
		{a.t(lang, "hamnet_address")},
		{a.t(lang, "local_ssh")},
		{a.t(lang, "hamnet_ssh")},
		{"- Local net schedules"},
		{"- Repeater status"},
		{"- Packet, APRS, and mesh experiments"},
	})
}

func (a *app) about(lang string) {
	a.showInfo(lang, a.t(lang, "about_title"), [][]string{{a.t(lang, "about_transport")}, {a.t(lang, "about_hamnet")}, {a.t(lang, "about_storage")}, {a.t(lang, "about_graphics")}, {a.t(lang, "about_note")}})
}
