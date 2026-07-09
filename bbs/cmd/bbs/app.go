package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func newApp() (*app, error) {
	dataDir := env("BBS_DATA_DIR", "/var/lib/bbs")
	port, _ := strconv.Atoi(env("APRS_IS_PORT", "14580"))
	cfg := config{
		dataDir:              dataDir,
		dbFile:               env("BBS_DB_FILE", filepath.Join(dataDir, "bbs.sqlite")),
		aprsLogFile:          filepath.Join(dataDir, "aprs", "aprs.log"),
		bbsLogFile:           filepath.Join(dataDir, "bbs.log"),
		transFile:            env("BBS_TRANSLATIONS_FILE", "/usr/local/bin/translations.json"),
		name:                 env("BBS_NAME", "HAMNET RADIO BBS"),
		sysopName:            env("BBS_SYSOP", "Sysop"),
		sysops:               parseSysops(os.Getenv("BBS_SYSOPS")),
		location:             env("BBS_LOCATION", "HamNet"),
		topic:                env("BBS_WELCOME_TOPIC", "Amateur radio notes, local nets, and packet-era experiments"),
		aprsServer:           env("APRS_IS_SERVER", "rotate.aprs2.net"),
		aprsPort:             port,
		aprsReceiverCallsign: env("APRS_RECEIVER_CALLSIGN", ""),
	}
	text := map[string]map[string]any{}
	if err := readJSON(cfg.transFile, &text, map[string]map[string]any{}); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	db, err := openDatabase(cfg.dbFile)
	if err != nil {
		return nil, err
	}
	return &app{cfg: cfg, text: text, db: db}, nil
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func parseSysops(raw string) map[string]bool {
	sysops := map[string]bool{}
	for _, item := range strings.Split(raw, ",") {
		item = normalizeCallsign(item)
		if item != "" {
			sysops[item] = true
		}
	}
	return sysops
}

func (a *app) t(lang, key string) string {
	if byLang, ok := a.text[lang]; ok {
		if value, ok := byLang[key].(string); ok {
			return value
		}
	}
	if byLang, ok := a.text["en"]; ok {
		if value, ok := byLang[key].(string); ok {
			return value
		}
	}
	return key
}

func (a *app) tList(lang, key string) []string {
	raw := a.text["en"][key]
	if byLang, ok := a.text[lang]; ok {
		if value, ok := byLang[key]; ok {
			raw = value
		}
	}
	items, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, fmt.Sprint(item))
	}
	return out
}

func (a *app) banner(lang string) string {
	line := a.cfg.name
	if a.currentUser != "" {
		userText := fmt.Sprintf("%s: %s", a.t(lang, "current_user_label"), a.currentUser)
		userWidth := lipgloss.Width(userText)
		titleWidth := panelContentWidth - userWidth - 1
		if titleWidth < 1 {
			titleWidth = 1
		}
		line = truncateText(line, titleWidth)
		spaces := panelContentWidth - lipgloss.Width(line) - userWidth
		if spaces < 1 {
			spaces = 1
		}
		line += strings.Repeat(" ", spaces) + userText
	}
	return titleStyle.Render(line) + "\n" + subtitleStyle.Render(a.cfg.location+" - "+a.cfg.topic) + "\n\n"
}

func (a *app) seedData() error {
	if err := os.MkdirAll(a.cfg.dataDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(a.cfg.dataDir, "aprs"), 0o755); err != nil {
		return err
	}
	if err := a.seedDefaultData(); err != nil {
		return err
	}
	return a.normalizeReceivedAPRSStore()
}

func (a *app) run() error {
	callsign, profile, err := a.authenticate()
	if err != nil {
		if errors.Is(err, errLoginCancelled) {
			return nil
		}
		return err
	}
	lang := profile.Language
	if lang == "" {
		lang = "en"
	}
	a.currentUser = callsign
	a.logBBSAction(callsign, "login", "sysop=%t", a.isSysop(callsign, profile))
	for {
		header := fmt.Sprintf("%s %s", a.t(lang, "logged_in_as"), callsign)
		if a.isSysop(callsign, profile) {
			header += " [" + a.t(lang, "sysop_role") + "]"
		}
		header += "    " + a.t(lang, "sysop") + ": " + a.cfg.sysopName
		opts := []option{
			{"1", a.t(lang, "menu_bulletins")},
			{"2", a.t(lang, "menu_messages")},
			{"3", a.t(lang, "menu_directory")},
			{"4", a.t(lang, "menu_profile")},
			{"5", a.t(lang, "menu_resources")},
			{"6", a.t(lang, "menu_aprs")},
			{"7", a.t(lang, "menu_about")},
		}
		if a.isSysop(callsign, profile) {
			opts = append(opts, option{"8", a.t(lang, "menu_sysop")})
		}
		opts = append(opts, option{"q", a.t(lang, "menu_quit")})
		choice := a.runMenu(lang, a.t(lang, "select"), header, opts)
		switch choice {
		case "1":
			a.showBulletins(lang)
		case "2":
			a.showMessages(callsign, lang)
		case "3":
			a.stationDirectory(lang)
		case "4":
			profile = a.changeProfile(callsign, profile, lang)
			lang = profile.Language
		case "5":
			a.radioResources(lang)
		case "6":
			profile = a.aprsMenu(callsign, profile, lang)
		case "7":
			a.about(lang)
		case "8":
			if a.isSysop(callsign, profile) {
				a.sysopMenu(callsign, lang)
			}
		case "q":
			a.logBBSAction(callsign, "logout", "")
			fmt.Println(a.banner(lang) + a.t(lang, "goodbye"))
			return nil
		}
	}
}
