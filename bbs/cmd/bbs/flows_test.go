package main

import (
	"strings"
	"testing"
	"time"
)

func installBasicText(a *app) {
	a.text = map[string]map[string]any{"en": {
		"login_info":                  []any{"Info"},
		"callsign_prompt":             "Callsign",
		"invalid_callsign":            "Invalid callsign",
		"password":                    "Password",
		"wrong_password":              "Wrong password",
		"too_many_attempts":           "Too many attempts",
		"user_disabled":               "Disabled",
		"registration_form_title":     "Registration",
		"profile_form_title":          "Profile",
		"profile_updated":             "Updated",
		"full_name":                   "Full name",
		"email":                       "Email",
		"invalid_email":               "Invalid email",
		"maidenhead":                  "Locator",
		"invalid_locator":             "Invalid locator",
		"language":                    "Language",
		"enable_aprs":                 "Enable APRS",
		"qth":                         "QTH",
		"rig":                         "Rig",
		"new_password":                "New password",
		"verify_password":             "Verify password",
		"required":                    "required",
		"password_required":           "Password required",
		"password_short":              "Password short",
		"password_mismatch":           "Password mismatch",
		"menu_directory":              "Directory",
		"resources_title":             "Resources",
		"about_title":                 "About",
		"hamnet_address":              "HamNet address",
		"local_ssh":                   "Local SSH",
		"hamnet_ssh":                  "HamNet SSH",
		"about_transport":             "Transport",
		"about_hamnet":                "HamNet",
		"about_storage":               "Storage",
		"about_graphics":              "Graphics",
		"about_note":                  "Note",
		"menu_bulletins":              "Bulletins",
		"no_bulletins":                "No bulletins",
		"sysop_manage_bulletins":      "Manage bulletins",
		"add_new_bulletin":            "Add bulletin",
		"bulletin_form_title":         "Bulletin",
		"bulletin_edit_title":         "Edit bulletin",
		"bulletin_title":              "Title",
		"bulletin_body":               "Body",
		"bulletin_published":          "Published",
		"bulletin_updated":            "Updated",
		"bulletin_deleted":            "Deleted",
		"confirm_delete_bulletin":     "Delete %s?",
		"at":                          "At",
		"from":                        "From",
		"message_boards_title":        "Boards",
		"select_board":                "Select board",
		"select_board_post":           "Select board for post",
		"select_board_delete":         "Select board to delete",
		"select_board_rename":         "Select board to rename",
		"select_board_message_delete": "Select message board",
		"select_message_delete":       "Select message",
		"no_boards":                   "No boards",
		"no_messages":                 "No messages",
		"message_form_title":          "Message",
		"message_reply_title":         "Reply",
		"message_edit_title":          "Edit message",
		"message_body":                "Body",
		"message_board":               "Board",
		"menu_post":                   "Post",
		"subject":                     "Subject",
		"message_posted":              "Posted",
		"message_reply_posted":        "Reply posted",
		"confirm_delete_message":      "Delete %s?",
		"back_button":                 "Back",
		"reply_button":                "Reply",
		"menu_quit":                   "Quit",
		"sysop_menu_title":            "Sysop",
		"sysop_list_users":            "Users",
		"sysop_toggle_sysop":          "Toggle sysop",
		"sysop_add_board":             "Add board",
		"sysop_delete_board":          "Delete board",
		"sysop_rename_board":          "Rename board",
		"sysop_delete_message":        "Edit message",
		"board_form_title":            "Board",
		"board_rename_title":          "Rename board",
		"board_name":                  "Name",
		"board_description":           "Description",
		"board_exists":                "Board exists",
		"cannot_delete_last_board":    "Cannot delete last board",
		"confirm_delete_board":        "Delete %s?",
		"target_callsign":             "Callsign",
		"last_connection":             "Last connection",
		"account_status":              "Status",
		"role":                        "Role",
		"user_role":                   "User",
		"sysop_role":                  "Sysop",
		"enabled":                     "Enabled",
		"disabled":                    "Disabled",
		"user_detail_title":           "User detail",
		"user_not_found":              "Not found",
		"user_updated":                "User updated",
		"user_deleted":                "User deleted",
		"cannot_manage_self":          "Cannot manage self",
		"cannot_remove_last_sysop":    "Cannot remove last sysop",
		"cannot_demote_configured":    "Cannot demote configured",
		"confirm_delete_user":         "Delete user %s?",
		"confirm_delete_field":        "Confirm deletion",
		"sysop":                       "Sysop",
	}}
}

func formValues(overrides map[string]string) map[string]string {
	values := map[string]string{
		"full_name":       "Volker",
		"email":           "volker@example.com",
		"maidenhead":      "IM77AH",
		"language":        "en",
		"enable_aprs":     "true",
		"qth":             "Home",
		"rig":             "Radio",
		"new_password":    "password1",
		"verify_password": "password1",
	}
	for key, value := range overrides {
		values[key] = value
	}
	return values
}

func TestAuthenticationRegistrationAndProfileFlows(t *testing.T) {
	a := testApp(t)
	installBasicText(a)
	formQueue := []struct {
		action string
		values map[string]string
		ok     bool
	}{
		{"submit", map[string]string{"callsign": "ea7klk", "password": ""}, true},
		{"save", formValues(nil), true},
	}
	a.runFormHook = func(lang, title string, fields []formField, buttons []string) (string, map[string]string, bool) {
		if len(formQueue) == 0 {
			t.Fatalf("unexpected form %s", title)
		}
		next := formQueue[0]
		formQueue = formQueue[1:]
		return next.action, next.values, next.ok
	}
	callsign, profile, err := a.authenticate()
	if err != nil {
		t.Fatal(err)
	}
	if callsign != "EA7KLK" || !profile.EnableAPRS || !verifyPassword("password1", profile.PasswordHash) {
		t.Fatalf("authenticate new user callsign=%q profile=%#v", callsign, profile)
	}

	formQueue = []struct {
		action string
		values map[string]string
		ok     bool
	}{
		{"submit", map[string]string{"callsign": "EA7KLK", "password": "password1"}, true},
	}
	callsign, profile, err = a.authenticate()
	if err != nil || callsign != "EA7KLK" || profile.LastSeen == "" {
		t.Fatalf("authenticate existing callsign=%q profile=%#v err=%v", callsign, profile, err)
	}

	formQueue = []struct {
		action string
		values map[string]string
		ok     bool
	}{
		{"submit", map[string]string{"callsign": "EA7KLK", "password": "bad"}, true},
		{"submit", map[string]string{"callsign": "EA7KLK", "password": "bad"}, true},
		{"submit", map[string]string{"callsign": "EA7KLK", "password": "bad"}, true},
	}
	a.showInfoHook = func(lang, title string, rows [][]string) {}
	if _, _, err := a.authenticate(); err == nil || !strings.Contains(err.Error(), "Too many") {
		t.Fatalf("authenticate wrong password err=%v", err)
	}

	users, _ := a.loadUsers()
	profile = users["EA7KLK"]
	a.runFormHook = func(lang, title string, fields []formField, buttons []string) (string, map[string]string, bool) {
		return "save", formValues(map[string]string{"full_name": "Updated", "new_password": "", "verify_password": ""}), true
	}
	updated := a.changeProfile("EA7KLK", profile, "en")
	if updated.FullName != "Updated" || !verifyPassword("password1", updated.PasswordHash) {
		t.Fatalf("changeProfile updated=%#v", updated)
	}
}

func TestScreenAndBulletinFlows(t *testing.T) {
	a := testApp(t)
	installBasicText(a)
	if err := a.saveUsers(map[string]userProfile{
		"EA7KLK": {FullName: "Volker", Maidenhead: "IM77AH", Language: "en"},
		"EA1OFF": {FullName: "Disabled", Disabled: true},
	}); err != nil {
		t.Fatal(err)
	}
	infoTitles := []string{}
	a.showInfoHook = func(lang, title string, rows [][]string) {
		infoTitles = append(infoTitles, title)
	}
	a.stationDirectory("en")
	a.radioResources("en")
	a.about("en")
	if len(infoTitles) != 3 || infoTitles[0] != "Directory" || infoTitles[1] != "Resources" || infoTitles[2] != "About" {
		t.Fatalf("screen info titles = %#v", infoTitles)
	}

	menuQueue := []string{"a", "1", "q"}
	formQueue := []struct {
		action string
		values map[string]string
		ok     bool
	}{
		{"save", map[string]string{"title": "Bulletin", "body": "Body"}, true},
		{"save", map[string]string{"title": "Edited", "body": "New body"}, true},
	}
	a.runMenuHook = func(lang, title, header string, opts []option) string {
		next := menuQueue[0]
		menuQueue = menuQueue[1:]
		return next
	}
	a.runFormHook = func(lang, title string, fields []formField, buttons []string) (string, map[string]string, bool) {
		next := formQueue[0]
		formQueue = formQueue[1:]
		return next.action, next.values, next.ok
	}
	a.manageBulletins("EA7KLK", "en")
	bulletins, _ := a.loadBulletins()
	if len(bulletins) != 1 || bulletins[0].Title != "Edited" {
		t.Fatalf("manageBulletins save/edit = %#v", bulletins)
	}

	menuQueue = []string{"1"}
	a.showInfoHook = func(lang, title string, rows [][]string) {
		if title != "Edited" {
			t.Fatalf("showBulletins title=%q", title)
		}
	}
	a.showBulletins("en")

	a.showInfoHook = func(lang, title string, rows [][]string) {}
	formQueue = []struct {
		action string
		values map[string]string
		ok     bool
	}{{"delete", nil, false}}
	a.confirmDeleteHook = func(lang, prompt string) bool { return true }
	a.editBulletinAt("EA7KLK", "en", bulletins, 0)
	bulletins, _ = a.loadBulletins()
	if len(bulletins) != 0 {
		t.Fatalf("bulletin delete = %#v", bulletins)
	}
}

func TestBoardAndSysopFlows(t *testing.T) {
	a := testApp(t)
	installBasicText(a)
	a.cfg.bbsLogFile = t.TempDir() + "/bbs.log"
	if err := a.saveUsers(map[string]userProfile{
		"EA7KLK": {FullName: "Volker", Email: "v@example.com", Language: "en", IsSysop: true},
		"EA1ABC": {FullName: "Alice", Email: "a@example.com", Language: "en", LastSeen: "2026-07-10 10:00 UTC"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.saveBoards(boardsData{Boards: []board{{ID: "general", Name: "General", Description: "General", Messages: []message{{From: "EA1ABC", Subject: "Root", Body: "Body", Created: now()}}}}}); err != nil {
		t.Fatal(err)
	}

	menuQueue := []string{"1", "p", "1", "r", "q"}
	formQueue := []struct {
		action string
		values map[string]string
		ok     bool
	}{
		{"save", map[string]string{"subject": "Posted", "body": "Posted body"}, true},
		{"reply", map[string]string{"subject": "Re: Posted", "body": "Reply body"}, true},
	}
	a.runMenuHook = func(lang, title, header string, opts []option) string {
		next := menuQueue[0]
		menuQueue = menuQueue[1:]
		return next
	}
	a.runFormHook = func(lang, title string, fields []formField, buttons []string) (string, map[string]string, bool) {
		next := formQueue[0]
		formQueue = formQueue[1:]
		return next.action, next.values, next.ok
	}
	a.showInfoHook = func(lang, title string, rows [][]string) {}
	a.showInfoActionsHook = func(lang, title string, rows [][]string, actions []option) string { return "r" }
	a.showMessages("EA7KLK", "en")
	boards, _ := a.loadBoards()
	if totalMessages(boards.Boards[0].Messages) != 3 {
		t.Fatalf("showMessages post/reply boards=%#v", boards.Boards[0].Messages)
	}

	a.runFormHook = func(lang, title string, fields []formField, buttons []string) (string, map[string]string, bool) {
		return "save", map[string]string{"name": "APRS", "description": "Traffic"}, true
	}
	a.addBoard("EA7KLK", "en")
	boards, _ = a.loadBoards()
	if len(boards.Boards) != 2 || boards.Boards[1].ID != "aprs" {
		t.Fatalf("addBoard boards=%#v", boards.Boards)
	}
	a.runMenuHook = func(lang, title, header string, opts []option) string { return "2" }
	a.runFormHook = func(lang, title string, fields []formField, buttons []string) (string, map[string]string, bool) {
		return "save", map[string]string{"name": "APRS Renamed"}, true
	}
	a.renameBoard("EA7KLK", "en")
	boards, _ = a.loadBoards()
	if boards.Boards[1].Name != "APRS Renamed" {
		t.Fatalf("renameBoard boards=%#v", boards.Boards)
	}
	a.confirmDeleteHook = func(lang, prompt string) bool { return true }
	a.deleteBoard("EA7KLK", "en")
	boards, _ = a.loadBoards()
	if len(boards.Boards) != 1 {
		t.Fatalf("deleteBoard boards=%#v", boards.Boards)
	}

	a.runMenuHook = func(lang, title, header string, opts []option) string { return "1" }
	a.toggleSysop("EA7KLK", "en", map[string]userProfile{"EA7KLK": {IsSysop: true}, "EA1ABC": {}})
	users, _ := a.loadUsers()
	if !users["EA1ABC"].IsSysop {
		t.Fatalf("toggleSysop users=%#v", users)
	}

	rows := userDetailRows(a, "en", "EA1ABC", users["EA1ABC"], true)
	if len(rows) < 4 || rows[0][1] != "EA1ABC" {
		t.Fatalf("userDetailRows=%#v", rows)
	}
	fields := a.userDetailFields("en", users["EA1ABC"])
	if len(fields) != 8 {
		t.Fatalf("userDetailFields=%#v", fields)
	}
	if row := userListRow("EA1ABC", "", "Enabled", "User", strings.Repeat("Name ", 20), 20); len([]rune(row)) > 80 {
		t.Fatalf("userListRow too long: %q", row)
	}
	if paddedCell("abcdef", 3) != "abc" || len(paddedCell("a", 3)) != 3 {
		t.Fatal("paddedCell failed")
	}

	a.runFormHook = func(lang, title string, fields []formField, buttons []string) (string, map[string]string, bool) {
		return "save", map[string]string{
			"full_name": "Alice Updated", "email": "a@example.com", "maidenhead": "", "language": "en", "enable_aprs": "false", "account_status": "disabled", "qth": "", "rig": "",
		}, true
	}
	a.editUserDetail("EA7KLK", "en", users, "EA1ABC")
	users, _ = a.loadUsers()
	if !users["EA1ABC"].Disabled || users["EA1ABC"].FullName != "Alice Updated" {
		t.Fatalf("editUserDetail users=%#v", users)
	}
	a.runFormHook = func(lang, title string, fields []formField, buttons []string) (string, map[string]string, bool) {
		return "delete", nil, false
	}
	a.editUserDetail("EA7KLK", "en", users, "EA1ABC")
	users, _ = a.loadUsers()
	if _, ok := users["EA1ABC"]; ok {
		t.Fatalf("delete user users=%#v", users)
	}

	a.runMenuHook = func(lang, title, header string, opts []option) string { return "1" }
	a.runFormHook = func(lang, title string, fields []formField, buttons []string) (string, map[string]string, bool) {
		return "delete", nil, false
	}
	a.editBoardMessage("EA7KLK", "en")
	boards, _ = a.loadBoards()
	if totalMessages(boards.Boards[0].Messages) != 1 {
		t.Fatalf("editBoardMessage delete boards=%#v", boards.Boards[0].Messages)
	}
}

func TestAPRSMenusAndRows(t *testing.T) {
	a := testApp(t)
	installBasicText(a)
	a.text["en"]["aprs_status"] = "APRS status"
	a.text["en"]["aprs_ssid_info"] = "SSID info"
	a.text["en"]["menu_aprs"] = "APRS"
	a.text["en"]["aprs_received_messages"] = "Received"
	a.text["en"]["aprs_sent_messages"] = "Sent"
	a.text["en"]["aprs_send_message"] = "Send"
	a.text["en"]["aprs_join_aprs_thursday"] = "APRSThursday"
	a.text["en"]["aprs_join_aprsph"] = "APRSPH"
	a.text["en"]["aprs_set_enabled"] = "Enable"
	a.text["en"]["aprs_enable_title"] = "Enable APRS"
	a.text["en"]["aprs_enable_required"] = "Enable required"
	a.text["en"]["aprs_latest_sent"] = "Latest sent"
	a.text["en"]["aprs_latest_received"] = "Latest received"
	a.text["en"]["aprs_no_sent_messages"] = "No sent"
	a.text["en"]["aprs_no_received_messages"] = "No received"
	a.text["en"]["aprs_parts"] = "parts"
	a.text["en"]["aprs_sent_status_sent"] = "Sent"
	a.text["en"]["aprs_sent_status_failed"] = "Failed"
	if err := a.saveUsers(map[string]userProfile{"EA7KLK": {Language: "en", EnableAPRS: false}}); err != nil {
		t.Fatal(err)
	}
	menuQueue := []string{"6", "q"}
	a.runMenuHook = func(lang, title, header string, opts []option) string {
		next := menuQueue[0]
		menuQueue = menuQueue[1:]
		return next
	}
	a.runFormHook = func(lang, title string, fields []formField, buttons []string) (string, map[string]string, bool) {
		return "save", map[string]string{"enable_aprs": "true"}, true
	}
	profile := a.aprsMenu("EA7KLK", userProfile{Language: "en"}, "en")
	if !profile.EnableAPRS {
		t.Fatalf("aprsMenu profile=%#v", profile)
	}
	sent := sentAPRS{At: "2026", From: "EA7KLK-0", To: "EA1ABC", Text: "Hello\nWorld", Status: aprsStatusSent, Acked: true, Parts: []sentAPRSPart{{Number: 1, Acked: true}}}
	received := receivedAPRS{At: "2026", From: "EA1ABC", To: "EA7KLK-0", Text: "Hello{12"}
	if rows := a.aprsSentRows("en", true, []sentAPRS{sent}); len(rows) < 4 {
		t.Fatalf("aprsSentRows=%#v", rows)
	}
	if rows := a.aprsReceivedRows("en", true, []receivedAPRS{received}); len(rows) < 4 {
		t.Fatalf("aprsReceivedRows=%#v", rows)
	}
	if !strings.Contains(a.sentAPRSSummary("en", sent), "Sent") || !strings.Contains(a.receivedAPRSSummary(received), "Hello") {
		t.Fatal("APRS summaries failed")
	}
	if !strings.Contains(a.sentAPRSListLabel(sent), "✓") || !strings.Contains(a.receivedAPRSListLabel(received), "EA1ABC") {
		t.Fatal("APRS list labels failed")
	}
	if a.aprsStatusLabel("en", "fallido") != "Failed" || normalizeAPRSStatus("unknown") != "unknown" {
		t.Fatal("APRS status normalization failed")
	}
	if sentAckIcon(sentAPRS{}) != "" || sentAckIcon(sentAPRS{Parts: []sentAPRSPart{{Acked: true}, {Acked: false}}}) != "?" {
		t.Fatal("sentAckIcon failed")
	}
	if reverseSent([]sentAPRS{{At: "1"}, {At: "2"}})[0].At != "2" || reverseReceived([]receivedAPRS{{At: "1"}, {At: "2"}})[0].At != "2" {
		t.Fatal("reverse history helpers failed")
	}
	a.showInfoActionsHook = func(lang, title string, rows [][]string, actions []option) string { return "q" }
	a.showSentAPRS("EA7KLK", userProfile{EnableAPRS: false}, "en")
	a.showReceivedAPRS("EA7KLK", userProfile{EnableAPRS: false}, "en")
	a.joinAPRSPH("EA7KLK", userProfile{EnableAPRS: false}, "en")
	a.sendAPRS("EA7KLK", userProfile{EnableAPRS: false}, "en")
	_ = time.Now()
}

func TestAPRSEditableFlowsWithHooks(t *testing.T) {
	a := testApp(t)
	installBasicText(a)
	for _, key := range []string{
		"aprs_send_message", "aprs_destination_callsign", "aprs_destination_ssid", "aprs_text", "aprs_invalid_destination",
		"aprs_invalid_ssid", "aprs_send_success", "aprs_send_failed", "aprs_retry_prompt", "retry_button", "cancel_button",
		"ok_button", "aprs_parts", "aprs_sent_status_sent", "aprs_sent_status_failed", "aprs_sent_message_detail",
		"aprs_received_message_detail", "delete_button", "reply_button", "back_button", "confirm_delete_aprs_message",
		"aprs_no_sent_messages", "aprs_no_received_messages", "aprs_latest_sent", "aprs_latest_received", "aprs_enable_required",
		"aprs_status", "aprs_ssid_info", "aprs_ansrvr_text", "aprs_join_aprsph", "aprs_join_aprs_thursday", "aprs_not_thursday_warning",
	} {
		a.text["en"][key] = key
	}
	a.cfg.aprsLogFile = t.TempDir() + "/aprs.log"
	a.cfg.bbsLogFile = t.TempDir() + "/bbs.log"

	packets := withFakeAPRSIS(t, 1)
	a.runFormHook = func(lang, title string, fields []formField, buttons []string) (string, map[string]string, bool) {
		return "send", map[string]string{"destination": "ea1abc", "destination_ssid": "", "text": "Hello"}, true
	}
	a.showInfoActionsHook = func(lang, title string, rows [][]string, actions []option) string { return "o" }
	if !a.sendAPRSForm("EA7KLK", userProfile{EnableAPRS: true}, "en", "") {
		t.Fatal("sendAPRSForm returned false for successful send")
	}
	if got := <-packets; len(got) != 2 || !strings.Contains(got[1], "::EA1ABC  ") {
		t.Fatalf("sendAPRSForm packet=%#v", got)
	}
	if got := len(a.loadSentHistory("EA7KLK")); got != 1 {
		t.Fatalf("sent history after send = %d", got)
	}

	packets = withFakeAPRSIS(t, 1)
	a.runFormHook = func(lang, title string, fields []formField, buttons []string) (string, map[string]string, bool) {
		return "send", map[string]string{"destination": "ea1abc", "destination_ssid": "", "text": ""}, true
	}
	if !a.sendAPRSForm("EA7KLK", userProfile{EnableAPRS: true}, "en", "") {
		t.Fatal("sendAPRSForm returned false for empty message")
	}
	if got := <-packets; len(got) != 2 || !strings.Contains(got[1], "::EA1ABC   :{") {
		t.Fatalf("empty APRS packet=%#v", got)
	}

	packets = withFakeAPRSIS(t, 1)
	a.runFormHook = func(lang, title string, fields []formField, buttons []string) (string, map[string]string, bool) {
		return "send", map[string]string{"text": "Checking in"}, true
	}
	if !a.sendANSRVRMessage("EA7KLK", userProfile{EnableAPRS: true}, "en", "ANSRVR", "CQ") {
		t.Fatal("sendANSRVRMessage returned false")
	}
	if got := <-packets; len(got) != 2 || !strings.Contains(got[1], ":CQ Checking in{") {
		t.Fatalf("ANSRVR packet=%#v", got)
	}

	packets = withFakeAPRSIS(t, 1)
	a.runFormHook = func(lang, title string, fields []formField, buttons []string) (string, map[string]string, bool) {
		return "send", map[string]string{"text": "Thursday check-in"}, true
	}
	if !a.sendANSRVRMessage("EA7KLK", userProfile{EnableAPRS: true}, "en", "APRSThursday", "CQ HOTG") {
		t.Fatal("sendANSRVRMessage returned false for APRSThursday")
	}
	if got := <-packets; len(got) != 2 || !strings.Contains(got[1], ":CQ HOTG Thursday check-in{") {
		t.Fatalf("APRSThursday ANSRVR packet=%#v", got)
	}

	sentRows := a.loadSentHistory("EA7KLK")
	a.runMenuHook = func(lang, title, header string, opts []option) string { return "1" }
	a.showInfoActionsHook = func(lang, title string, rows [][]string, actions []option) string { return "d" }
	a.confirmDeleteHook = func(lang, prompt string) bool { return true }
	a.showSentAPRS("EA7KLK", userProfile{EnableAPRS: true}, "en")
	if len(sentRows) == 0 || len(a.loadSentHistory("EA7KLK")) >= len(sentRows) {
		t.Fatalf("showSentAPRS delete did not remove a row")
	}

	if _, err := a.addReceivedRecord("EA7KLK", receivedAPRS{At: now(), From: "EA1ABC-0", To: "EA7KLK-0", Text: "Reply?", Raw: "raw"}); err != nil {
		t.Fatal(err)
	}
	receivedActions := []string{"r", "q"}
	a.showInfoActionsHook = func(lang, title string, rows [][]string, actions []option) string {
		next := receivedActions[0]
		receivedActions = receivedActions[1:]
		return next
	}
	a.runFormHook = func(lang, title string, fields []formField, buttons []string) (string, map[string]string, bool) {
		return "cancel", nil, false
	}
	receivedMenus := []string{"1", "q"}
	a.runMenuHook = func(lang, title, header string, opts []option) string {
		next := receivedMenus[0]
		receivedMenus = receivedMenus[1:]
		return next
	}
	a.showReceivedAPRS("EA7KLK", userProfile{EnableAPRS: true}, "en")

	a.showInfoActionsHook = func(lang, title string, rows [][]string, actions []option) string { return "q" }
	a.joinAPRSThursday("EA7KLK", userProfile{EnableAPRS: true}, "en")
}
