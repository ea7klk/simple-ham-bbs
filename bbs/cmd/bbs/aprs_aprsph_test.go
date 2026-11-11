package main

import (
	"strings"
	"testing"
)

// The APRSPH command mapping. Every entry the menu offers must produce a command
// the bot documents, and none may be longer than one APRS message.

func TestAPRSPHCommandsMatchTheBotDocumentation(t *testing.T) {
	cases := []struct {
		kind, count, target, phrase string
		thursday                    bool
		want                        string
	}{
		{"checkin", "", "", "Hello all", false, "CQ Hello all"},
		{"checkin", "", "", "", false, "CQ"},
		{"quiet", "", "", "Hello all", false, "NET Hello all"},
		{"quiet", "", "", "", false, "NET"},
		{"list", "", "", "", false, "LIST"},

		{"last", "1", "", "", false, "LAST"},
		{"last", "2", "", "", false, "LAST10"},
		{"last", "3", "", "", false, "LAST15"},
		{"last", "4", "", "", true, "LAST THURS"},
		{"last", "5", "", "", true, "LAST10 THURS"},
		{"last", "6", "", "", true, "LAST15 THURS"},

		{"mine", "1", "", "", false, "MINE"},
		{"mine", "2", "", "", false, "MINE10"},
		{"mine", "3", "", "", false, "MINE15"},
		{"mine", "1", "DV2XXR-7", "", false, "MINE DV2XXR-7"},
		{"mine", "2", "DV2XXR-7", "", false, "MINE10 DV2XXR-7"},
		// The Thursday form takes precedence over a target callsign.
		{"mine", "1", "", "", true, "MINE THURS"},
		{"mine", "3", "", "", true, "MINE15 THURS"},

		{"search", "1", "", "typhoon", false, "SEARCH typhoon"},
		{"search", "2", "", "typhoon", false, "SEARCH10 typhoon"},
		{"search", "3", "", "typhoon", false, "SEARCH15 typhoon"},

		{"inbox", "", "", "", false, "?APRSM"},
		{"inbox", "", "DV2XXR-7", "", false, "?APRSM DV2XXR-7"},
		{"checkout", "", "", "73 all", false, "U 73 all"},
		{"checkout", "", "", "", false, "U"},
		{"path", "", "", "", false, "?APRST"},
		{"position", "", "", "", false, "?APRSP"},
		{"status", "", "", "", false, "?APRSS"},
		{"help", "", "", "", false, "HELP"},
	}
	for _, tc := range cases {
		got := aprsphCommandFor(tc.kind, tc.count, tc.target, tc.phrase, tc.thursday)
		if got != tc.want {
			t.Fatalf("%s(count=%s target=%q thursday=%v) = %q, want %q",
				tc.kind, tc.count, tc.target, tc.thursday, got, tc.want)
		}
		if !aprsphCommandFits(got) {
			t.Fatalf("command %q exceeds one APRS message", got)
		}
	}
}

// Every command label the menu shows must be translated in every language.
func TestAPRSPHLabelsAreTranslated(t *testing.T) {
	a := testApp(t)
	raw := map[string]map[string]any{}
	if err := readJSON("../../translations.json", &raw, map[string]map[string]any{}); err != nil {
		t.Fatal(err)
	}
	a.text = raw
	keys := []string{
		"aprsph_menu_title", "aprsph_menu_hint", "aprsph_checkin", "aprsph_quiet_checkin",
		"aprsph_checkin_text", "aprsph_checkin_text_optional", "aprsph_list",
		"aprsph_net_messages", "aprsph_my_messages", "aprsph_search", "aprsph_inbox",
		"aprsph_bot_info", "aprsph_bot_info_hint", "aprsph_checkout", "aprsph_checkout_text",
		"aprsph_help", "aprsph_last_five", "aprsph_last_ten", "aprsph_last_fifteen",
		"aprsph_last_five_thurs", "aprsph_last_ten_thurs", "aprsph_last_fifteen_thurs",
		"aprsph_count_hint", "aprsph_callsign_optional", "aprsph_thursday_only",
		"aprsph_search_prompt", "aprsph_path", "aprsph_position", "aprsph_status",
	}
	for _, lang := range languageOrder {
		for _, key := range keys {
			if a.t(lang, key) == key {
				t.Fatalf("missing translation %q for %q", key, lang)
			}
		}
		if len(a.tList(lang, "aprsph_help_lines")) == 0 {
			t.Fatalf("help lines missing for %q", lang)
		}
	}
}

// The command keywords themselves must never be translated: the bot matches the
// literal token, so a localised CQ would be ignored.
func TestAPRSPHCommandsAreNotTranslated(t *testing.T) {
	for _, command := range []string{"CQ", "NET", "LIST", "LAST", "MINE", "SEARCH", "U", "HELP", "?APRST", "?APRSP", "?APRSS", "?APRSM", "THURS"} {
		got := aprsphCommandFor(strings.ToLower(command), "", "", "", false)
		if got != "" && strings.ToUpper(got) != got {
			t.Fatalf("command %q was lower-cased: %q", command, got)
		}
	}
}

// The submenu sends the chosen command to APRSPH, not to ANSRVR.
func TestAPRSPHMenuSendsToTheBot(t *testing.T) {
	packets := withFakeAPRSIS(t, 1)
	a := testApp(t)
	a.cfg.aprsServer = "127.0.0.1"
	a.cfg.aprsPort = 14580
	a.cfg.aprsLogFile = t.TempDir() + "/aprs.log"
	installBasicText(a)
	a.text["en"]["aprsph_menu_title"] = "APRSPH net"
	a.text["en"]["aprsph_list"] = "Who is checked in"
	a.text["en"]["back_button"] = "Back"
	a.text["en"]["menu_quit"] = "Quit"
	a.text["en"]["aprs_status"] = "Status"
	a.text["en"]["aprs_ssid_info"] = "SSID"
	a.text["en"]["aprs_enable_required"] = "Enable APRS first"
	a.text["en"]["aprs_send_success"] = "Sent"
	a.text["en"]["ok_button"] = "OK"

	choices := []string{"3", "q"}
	a.runMenuHook = func(lang, title, header string, opts []option) string {
		next := choices[0]
		choices = choices[1:]
		return next
	}
	a.showInfoActionsHook = func(lang, title string, rows [][]string, actions []option) string { return "o" }

	a.aprsphManageMenu("EA7KLK", userProfile{EnableAPRS: true}, "en")

	got := <-packets
	if len(got) != 2 {
		t.Fatalf("packets = %#v", got)
	}
	if !strings.Contains(got[1], "::APRSPH   :LIST{") {
		t.Fatalf("LIST was not addressed to APRSPH: %q", got[1])
	}
}

// A disabled account is refused before anything is sent.
func TestAPRSPHRefusesDisabledAccounts(t *testing.T) {
	packets := withFakeAPRSIS(t, 1)
	a := testApp(t)
	a.cfg.aprsServer = "127.0.0.1"
	a.cfg.aprsPort = 14580
	a.cfg.aprsLogFile = t.TempDir() + "/aprs.log"
	installBasicText(a)
	a.text["en"]["aprsph_menu_title"] = "APRSPH net"
	a.text["en"]["aprsph_list"] = "Who is checked in"
	a.text["en"]["back_button"] = "Back"
	a.text["en"]["aprs_status"] = "Status"
	a.text["en"]["aprs_ssid_info"] = "SSID"
	a.text["en"]["aprs_enable_required"] = "Enable APRS first"

	choices := []string{"3", "q"}
	a.runMenuHook = func(lang, title, header string, opts []option) string {
		next := choices[0]
		choices = choices[1:]
		return next
	}
	a.showInfoHook = func(lang, title string, rows [][]string) {}

	a.aprsphManageMenu("EA7KLK", userProfile{EnableAPRS: false}, "en")
	select {
	case pkt := <-packets:
		t.Fatalf("a disabled account transmitted: %#v", pkt)
	default:
	}
}

// An over-long check-in is refused at the form, not truncated or split.
func TestAPRSPHCheckInEnforcesLength(t *testing.T) {
	a := testApp(t)
	installBasicText(a)
	a.text["en"]["aprsph_checkin"] = "Check in"
	a.text["en"]["aprsph_checkin_text"] = "Message"
	a.text["en"]["aprs_status"] = "Status"
	a.text["en"]["aprs_ssid_info"] = "SSID"
	a.text["en"]["aprs_enable_required"] = "Enable APRS"

	var captured []formField
	a.runFormHook = func(lang, title string, fields []formField, buttons []string) (string, map[string]string, bool) {
		captured = fields
		return "cancel", nil, false
	}
	a.aprsphCheckIn("EA7KLK", userProfile{EnableAPRS: true}, "en", false)

	if len(captured) != 1 || captured[0].validator == nil {
		t.Fatal("the check-in form has no length validator")
	}
	maxText := aprsCommandMaxBody - len("CQ") - 1
	if !captured[0].validator(strings.Repeat("x", maxText)) {
		t.Fatalf("a body of %d characters was rejected", maxText)
	}
	if captured[0].validator(strings.Repeat("x", maxText+1)) {
		t.Fatalf("a body of %d characters was accepted", maxText+1)
	}
}
