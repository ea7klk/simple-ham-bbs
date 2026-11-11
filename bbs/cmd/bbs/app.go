package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

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
	return &app{cfg: cfg, text: text, db: db, mail: newMailerFromEnv()}, nil
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

// initialize runs the one-shot startup work for this database: the schema
// migration and everything that used to be repeated by every SSH session.
//
// It is called once, by the supervisor or by `bbs_app migrate`, never from the
// connection path. Sessions call waitForSchema instead, which is one indexed
// read.
func (a *app) initialize() error {
	// Skip the expensive half when the schema is already current: the supervisor
	// restarts after a crash, and re-running AutoMigrate on every restart would
	// be work for nothing. seedData stays outside the guard because it seeds and
	// reports, not only migrates.
	//
	// The marker alone is not enough. It records that *a* migration ran, not that
	// this build's migration ran, so a table added without a version bump would
	// be missing while the marker looked current — which is exactly what
	// happened once. Checking the tables are actually present costs a handful of
	// sqlite_master lookups at supervisor start and turns that class of mistake
	// into a slower boot rather than a broken feature.
	missing := missingTables(a.db)
	if !schemaReady(a.db) || len(missing) > 0 {
		if len(missing) > 0 {
			a.logBBSAction("", "schema_repair", "missing_tables=%q", strings.Join(missing, ","))
		}
		if err := migrateDatabase(a.db); err != nil {
			return err
		}
	}
	return a.seedData()
}

// waitForSchema blocks until the one-shot migration has completed, then returns.
//
// A session process is not the right place to run a migration: AutoMigrate walks
// every table and the cost grows with the user count, and it takes the write
// lock. But a session must not run against an unmigrated file either, so it
// waits briefly for the supervisor — which starts first in the container — and
// then says plainly what to do rather than failing with a missing column.
func (a *app) waitForSchema() error {
	deadline := time.Now().Add(schemaWait)
	for {
		if schemaReady(a.db) {
			return nil
		}
		if time.Now().After(deadline) {
			a.logBBSAction("", "schema_not_ready", "waited=%s", schemaWait)
			return fmt.Errorf("database schema is not ready (no %s=%s in db_settings); run `bbs_app migrate`", schemaVersionKey, schemaVersion)
		}
		time.Sleep(schemaPollInterval)
	}
}

// seedData prepares the data directories, seeds the default board and bulletins,
// and performs the startup reporting and sweeps. It is part of the one-shot
// initialisation, not the connection path.
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
	a.reportMailState()
	a.reportBootstrapSysopState()
	if err := a.applyEmailChanges(time.Now()); err != nil {
		a.logBBSAction("", "email_change_apply_failed", "error=%q", err.Error())
	}
	return a.normalizeReceivedAPRSStore()
}

// reportMailState records, once per process, whether outbound mail is usable
// and why not when it is not.
//
// A deployment can declare every SMTP setting and still be unable to send — the
// password may still be a placeholder, or the URL may be malformed. Saying so at
// startup is the difference between "activation is silently skipped" and an
// operator who knows to finish the configuration.
func (a *app) reportMailState() {
	if problem := a.mail.Problem(); problem != "" {
		a.logBBSAction("", "mail_disabled", "reason=%q", problem)
		return
	}
	a.logBBSAction("", "mail_enabled", "host=%q from=%q security=%q", a.mail.cfg.host, a.mail.cfg.from, a.mail.cfg.security)
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

// terminalFailure turns a recorded terminal error into the error run() returns,
// so main can report it and exit non-zero instead of pretending the caller hung
// up.
func (a *app) terminalFailure() error {
	if a.terminalError == nil {
		return nil
	}
	return fmt.Errorf("terminal session ended unexpectedly: %w", a.terminalError)
}

func (a *app) run() error {
	callsign, profile, err := a.authenticate()
	if err != nil {
		if errors.Is(err, errLoginCancelled) {
			// A cancelled login is a clean exit — unless the terminal itself
			// failed, which is not the same thing.
			if a.terminalError != nil {
				return a.terminalFailure()
			}
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
			if a.terminalError != nil {
				return a.terminalFailure()
			}
			a.logBBSAction(callsign, "logout", "")
			fmt.Println(a.banner(lang) + a.t(lang, "goodbye"))
			return nil
		}
	}
}
