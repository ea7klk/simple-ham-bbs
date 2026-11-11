package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func newApp() (*app, error) {
	dataDir := env("BBS_DATA_DIR", "/var/lib/bbs")
	port, _ := strconv.Atoi(env("APRS_IS_PORT", "14580"))
	sysops, err := parseSysops(os.Getenv("BBS_SYSOPS"))
	if err != nil {
		return nil, err
	}
	cfg := config{
		dataDir:              dataDir,
		dbFile:               env("BBS_DB_FILE", filepath.Join(dataDir, "bbs.sqlite")),
		aprsLogFile:          filepath.Join(dataDir, "aprs", "aprs.log"),
		bbsLogFile:           filepath.Join(dataDir, "bbs.log"),
		authLogFile:          env("BBS_AUTH_LOG_FILE", "/var/log/auth.log"),
		transFile:            env("BBS_TRANSLATIONS_FILE", "/usr/local/bin/translations.json"),
		name:                 env("BBS_NAME", "HAMNET RADIO BBS"),
		sysopName:            env("BBS_SYSOP", "Sysop"),
		sysops:               sysops,
		location:             env("BBS_LOCATION", "HamNet"),
		topic:                env("BBS_WELCOME_TOPIC", "Amateur radio notes, local nets, and packet-era experiments"),
		aprsServer:           env("APRS_IS_SERVER", "rotate.aprs2.net"),
		aprsPort:             port,
		aprsReceiverCallsign: env("APRS_RECEIVER_CALLSIGN", ""),
	}
	// Operator-supplied labels reach the same renderers as user text, so clean
	// them once at startup rather than trusting every screen to remember.
	cfg.name = sanitizeSingleLineText(cfg.name)
	cfg.sysopName = sanitizeSingleLineText(cfg.sysopName)
	cfg.location = sanitizeSingleLineText(cfg.location)
	cfg.topic = sanitizeSingleLineText(cfg.topic)
	text := map[string]map[string]any{}
	if err := readJSON(cfg.transFile, &text, map[string]map[string]any{}); err != nil {
		// Keep running with the fallback map, but say so: a silent fallback
		// renders raw translation keys and looks like a UI bug.
		appendLogFile(cfg.bbsLogFile, fmt.Sprintf("%s config action=translations_parse_error file=%q error=%q\n", now(), cfg.transFile, err.Error()))
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

// parseSysops returns the configured bootstrap sysops and rejects entries that
// are not callsigns.
//
// A typo used to be accepted, normalised, and then silently never match, so an
// operator could believe they had a sysop when the variable granted nothing.
// Failing here turns that into a startup error naming the offending entries.
func parseSysops(raw string) (map[string]bool, error) {
	sysops := map[string]bool{}
	invalid := []string{}
	for _, item := range strings.Split(raw, ",") {
		item = normalizeCallsign(item)
		if item == "" {
			continue
		}
		if !callsignRE.MatchString(item) {
			invalid = append(invalid, item)
			continue
		}
		sysops[item] = true
	}
	if len(invalid) > 0 {
		return nil, fmt.Errorf("BBS_SYSOPS contains %d invalid callsign(s): %s (expected something like EA7KLK)", len(invalid), strings.Join(invalid, ", "))
	}
	return sysops, nil
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
		if a.currentUserIsSysop {
			// Marked on every screen, not just the main menu: a sysop should
			// never have to remember which session they are in.
			userText += " [" + a.t(lang, "sysop_role") + "]"
		}
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
	a.reportBootstrapSysopState()
	return a.normalizeReceivedAPRSStore()
}

// reportBootstrapSysopState records whether the configured bootstrap sysops are
// still waiting for an account.
//
// Since BBS_SYSOPS became a bootstrap-only grant, an operator who sets it and
// never logs in with one of those callsigns has no sysop at all — and nothing
// on screen would say so. This puts the state in the log once per process.
func (a *app) reportBootstrapSysopState() {
	if len(a.cfg.sysops) == 0 {
		return
	}
	users, err := a.loadUsers()
	if err != nil {
		a.logBBSAction("", "sysop_bootstrap_unknown", "error=%q", err.Error())
		return
	}
	if hasActiveSysop(users) {
		return
	}
	claimed := []string{}
	for callsign := range a.cfg.sysops {
		if _, ok := users[callsign]; ok {
			claimed = append(claimed, callsign)
		}
	}
	sort.Strings(claimed)
	if len(claimed) > 0 {
		a.logBBSAction("", "sysop_bootstrap_pending", "callsigns=%q note=%q", strings.Join(claimed, ","), "account exists but the sysop role is not granted until the next login")
		return
	}
	a.logBBSAction("", "sysop_bootstrap_unclaimed", "configured=%d note=%q", len(a.cfg.sysops), "no account holds the sysop role; the first listed callsign to log in is granted it")
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
	a.currentUserIsSysop = a.isSysop(profile)
	a.logBBSAction(callsign, "login", "sysop=%t", a.currentUserIsSysop)
	a.sendLoginAPRSBeacon(callsign, profile)
	for {
		// The role marker lives in the banner, which every screen renders.
		header := fmt.Sprintf("%s %s    %s: %s", a.t(lang, "logged_in_as"), callsign, a.t(lang, "sysop"), a.cfg.sysopName)
		opts := []option{
			{"1", a.t(lang, "menu_bulletins")},
			{"2", a.t(lang, "menu_messages")},
			{"3", a.t(lang, "menu_directory")},
			{"4", a.t(lang, "menu_profile")},
			{"5", a.t(lang, "menu_resources")},
			{"6", a.t(lang, "menu_aprs")},
			{"7", a.t(lang, "menu_about")},
		}
		if a.isSysop(profile) {
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
			a.currentUserIsSysop = a.isSysop(profile)
		case "5":
			a.radioResources(lang)
		case "6":
			profile = a.aprsMenu(callsign, profile, lang)
		case "7":
			a.about(lang)
		case "8":
			if a.isSysop(profile) {
				a.sysopMenu(callsign, lang)
			}
		case "q":
			a.logBBSAction(callsign, "logout", "")
			fmt.Println(a.banner(lang) + a.t(lang, "goodbye"))
			return nil
		}
	}
}
