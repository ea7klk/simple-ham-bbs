package main

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func testUIApp() *app {
	return &app{
		cfg: config{name: "BBS", location: "HamNet", topic: "Tests"},
		text: map[string]map[string]any{"en": {
			"current_user_label":     "User",
			"menu_hint_actions":      "TAB next ENTER choose ESC back",
			"form_hint_actions":      "TAB next Shift+TAB back ENTER choose ESC cancel",
			"form_hint_submit":       "ENTER submit ESC cancel",
			"form_more_fields_above": "...more fields above",
			"form_more_fields_below": "...more fields below",
			"info_hint_actions":      "Up/down scroll ENTER/q back",
			"info_more_above":        "...more above",
			"info_more_below":        "...more below",
			"required":               "required",
			"invalid_choice":         "invalid choice",
			"password_short":         "Password too short",
			"password_mismatch":      "Passwords do not match",
			"save_button":            "Save",
			"cancel_button":          "Cancel",
			"delete_button":          "Delete",
			"login_button":           "Login",
			"send_button":            "Send",
			"reply_button":           "Reply",
			"no_label":               "No",
			"yes_label":              "Yes",
		}},
	}
}

func key(name string) tea.KeyMsg {
	switch name {
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "shift+tab":
		return tea.KeyMsg{Type: tea.KeyShiftTab}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "pgup":
		return tea.KeyMsg{Type: tea.KeyPgUp}
	case "pgdown":
		return tea.KeyMsg{Type: tea.KeyPgDown}
	case " ":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(name)}
	}
}

func runeKey(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

func TestTerminalLayout(t *testing.T) {
	if screenWidth != 132 || screenHeight != 24 {
		t.Fatalf("terminal layout = %dx%d, want 132x24", screenWidth, screenHeight)
	}
	if panelContentWidth != 128 || panelContentHeight != 22 {
		t.Fatalf("panel content = %dx%d, want 128x22", panelContentWidth, panelContentHeight)
	}
	for name, style := range map[string]lipgloss.Style{"panel": panelStyle, "form": formPanelStyle} {
		view := style.Render("content")
		if lipgloss.Width(view) != screenWidth || lipgloss.Height(view) != screenHeight {
			t.Fatalf("%s panel size = %dx%d, want %dx%d", name, lipgloss.Width(view), lipgloss.Height(view), screenWidth, screenHeight)
		}
		for _, borderChar := range []string{"┌", "─", "┐", "│", "└", "┘"} {
			if !strings.Contains(view, borderChar) {
				t.Fatalf("%s panel is missing Unicode border character %q: %q", name, borderChar, view)
			}
		}
	}
	if got := clientTerminalResizeSequence(); got != "\033[8;24;132t" {
		t.Fatalf("resize sequence = %q", got)
	}
	msg := fixedTerminalSizeFilter(nil, tea.WindowSizeMsg{Width: 80, Height: 24}).(tea.WindowSizeMsg)
	if msg.Width != screenWidth || msg.Height != screenHeight {
		t.Fatalf("filtered terminal size = %dx%d", msg.Width, msg.Height)
	}
}

func TestMenuModelUpdateAndView(t *testing.T) {
	a := testUIApp()
	m := menuModel{app: a, lang: "en", title: "Menu", header: strings.Repeat("header\n", 8), options: []option{{"1", "One"}, {"2", "Two"}, {"q", "Quit"}}}
	if cmd := m.Init(); cmd != nil {
		t.Fatal("menu Init returned command")
	}
	model, _ := m.Update(key("down"))
	m = model.(menuModel)
	if m.cursor != 1 {
		t.Fatalf("cursor after down = %d", m.cursor)
	}
	model, _ = m.Update(key("up"))
	m = model.(menuModel)
	if m.cursor != 0 {
		t.Fatalf("cursor after up = %d", m.cursor)
	}
	model, _ = m.Update(runeKey('2'))
	m = model.(menuModel)
	if !m.done || m.chosen != "2" || m.cursor != 1 {
		t.Fatalf("shortcut choice model = %#v", m)
	}
	m = menuModel{app: a, lang: "en", title: "Menu", options: []option{{"1", "One"}}}
	model, _ = m.Update(key("esc"))
	m = model.(menuModel)
	if m.chosen != "q" || !m.done {
		t.Fatalf("esc model = %#v", m)
	}
	view := menuModel{app: a, lang: "en", title: "Menu", options: []option{{"1", "One"}, {"2", "Two"}}}.View()
	if !strings.Contains(view, "Menu") || !strings.Contains(view, "TAB next") {
		t.Fatalf("menu view = %q", view)
	}
	if start, end := visibleRange(20, 10, 5); start >= end || start < 0 || end > 20 {
		t.Fatalf("visibleRange = %d/%d", start, end)
	}
	if lineCount("a\nb\n") != 2 || limitLines("a\nb\nc", 2) == "a\nb\nc" {
		t.Fatal("lineCount/limitLines failed")
	}
	if lines := wrapText("one two three four", 10); len(lines) < 2 {
		t.Fatalf("wrapText = %#v", lines)
	}
	for _, line := range wrapText("12345678 💡", 10) {
		if lipgloss.Width(line) > 10 {
			t.Fatalf("wide-character wrapped line width = %d: %q", lipgloss.Width(line), line)
		}
	}
	if truncateText("abcdef", 4) != "a..." || truncateText("abcdef", 2) != "ab" {
		t.Fatal("truncateText failed")
	}
	if !strings.Contains(dimWrapped("one two", 10), "one") {
		t.Fatal("dimWrapped missing text")
	}
	short := renderMenuOption(option{value: "1", label: "Message"}, false)
	long := renderMenuOption(option{value: "10", label: "Message"}, false)
	if strings.Index(short, "Message") != strings.Index(long, "Message") {
		t.Fatalf("menu number column shifted label: short=%q long=%q", short, long)
	}
}

func TestMenuModelCanStartAtExistingMessage(t *testing.T) {
	a := testUIApp()
	m := menuModel{app: a, lang: "en", title: "Messages", options: []option{{"1", "One"}, {"2", "Two"}, {"3", "Three"}}, cursor: 2}
	model, _ := m.Update(key("enter"))
	chosen := model.(menuModel).chosen
	if chosen != "3" {
		t.Fatalf("menu initial cursor selected %q, want %q", chosen, "3")
	}
}

func TestMenuModelPageNavigation(t *testing.T) {
	a := testUIApp()
	options := make([]option, 100)
	for i := range options {
		options[i] = option{value: fmt.Sprintf("%d", i+1), label: fmt.Sprintf("Message %d", i+1)}
	}
	m := menuModel{app: a, lang: "en", title: "Messages", options: options, cursor: 50}
	model, _ := m.Update(key("pgup"))
	m = model.(menuModel)
	if m.cursor >= 50 || m.cursor < 0 {
		t.Fatalf("cursor after pgup = %d", m.cursor)
	}
	previous := m.cursor
	model, _ = m.Update(key("pgdown"))
	m = model.(menuModel)
	if m.cursor <= previous || m.cursor >= len(options) {
		t.Fatalf("cursor after pgdown = %d, previous=%d", m.cursor, previous)
	}
	m.cursor = 0
	model, _ = m.Update(key("pgup"))
	m = model.(menuModel)
	if m.cursor != 0 {
		t.Fatalf("cursor pgup at start = %d", m.cursor)
	}
	m.cursor = len(options) - 1
	model, _ = m.Update(key("pgdown"))
	m = model.(menuModel)
	if m.cursor != len(options)-1 {
		t.Fatalf("cursor pgdown at end = %d", m.cursor)
	}
}

func TestFormModelValidationNavigationAndRendering(t *testing.T) {
	a := testUIApp()
	m := newFormModel(a, "en", "Form", []formField{
		{name: "callsign", label: "Callsign", required: true, value: "ea7klk", normalizer: normalizeCallsign, validator: validAPRSBaseCallsign},
		{name: "mode", label: "Mode", kind: fieldChoice, value: "off", choices: []option{{"off", "Off"}, {"on", "On"}}},
		{name: "notes", label: "Notes", kind: fieldTextArea, value: "line one"},
		{name: "new_password", label: "Password", kind: fieldPassword},
		{name: "verify_password", label: "Verify", kind: fieldPassword},
	}, []string{"save", "cancel"})
	if m.focus != 0 || !m.fields[0].input.Focused() {
		t.Fatalf("initial focus = %d", m.focus)
	}
	model, _ := m.Update(key("tab"))
	m = model.(formModel)
	if m.focus != 1 {
		t.Fatalf("focus after tab = %d", m.focus)
	}
	model, _ = m.Update(key("right"))
	m = model.(formModel)
	if m.fields[1].value != "on" {
		t.Fatalf("choice after right = %q", m.fields[1].value)
	}
	model, _ = m.Update(key("shift+tab"))
	m = model.(formModel)
	if m.focus != 0 {
		t.Fatalf("focus after shift-tab = %d", m.focus)
	}
	values, errText := m.validate()
	if errText != "" || values["callsign"] != "EA7KLK" || values["mode"] != "on" {
		t.Fatalf("validate values=%#v err=%q", values, errText)
	}

	m.fields[0].input.SetValue("")
	if _, errText := m.validate(); !strings.Contains(errText, "required") {
		t.Fatalf("required validation err = %q", errText)
	}
	m.fields[0].input.SetValue("EA7KLK")
	m.fields[3].input.SetValue("short")
	m.fields[4].input.SetValue("short")
	if _, errText := m.validate(); !strings.Contains(errText, "short") {
		t.Fatalf("short password err = %q", errText)
	}
	m.fields[3].input.SetValue("longenough")
	m.fields[4].input.SetValue("different")
	if _, errText := m.validate(); !strings.Contains(errText, "match") {
		t.Fatalf("mismatch password err = %q", errText)
	}
	m.fields[4].input.SetValue("longenough")
	m.setFocus(len(m.fields))
	model, _ = m.Update(key("enter"))
	m = model.(formModel)
	if !m.done || m.action != "save" || m.values["new_password"] != "longenough" {
		t.Fatalf("save model = %#v", m)
	}

	cancel := newFormModel(a, "en", "Cancel", []formField{{name: "x", label: "X"}}, []string{"save", "cancel"})
	model, _ = cancel.Update(key("ctrl+c"))
	cancel = model.(formModel)
	if cancel.action != "cancel" || !cancel.done {
		t.Fatalf("ctrl+c cancel model = %#v", cancel)
	}
	submit := newFormModel(a, "en", "Login", []formField{{name: "callsign", label: "Callsign", required: true, value: "EA7KLK"}}, nil)
	model, _ = submit.Update(key("enter"))
	submit = model.(formModel)
	if !submit.done || submit.action != "submit" || submit.values["callsign"] != "EA7KLK" {
		t.Fatalf("submit model = %#v", submit)
	}

	render := newFormModel(a, "en", "Render", []formField{
		{name: "a", label: "A", value: "abcdef", width: 4, sameLine: true},
		{name: "b", label: "B", value: "ghij", width: 4, sameLine: true},
		{name: "area", label: "Area", kind: fieldTextArea, value: strings.Repeat("text ", 20)},
		{name: "choice", label: "Choice", kind: fieldChoice, value: "yes", choices: []option{{"yes", "Yes"}, {"no", "No"}}},
	}, []string{"save", "delete", "cancel"})
	render.setFocus(2)
	if start, end := render.visibleFieldRange(7); start > 2 || end <= 2 {
		t.Fatalf("visibleFieldRange = %d/%d", start, end)
	}
	if render.fieldLines(-1) != 0 || render.fieldLines(2) != formTextAreaHeight+1 || render.fieldLines(3) != 2 {
		t.Fatal("fieldLines failed")
	}
	if view := render.View(); !strings.Contains(view, "Render") || !strings.Contains(view, "Area") || !strings.Contains(view, "TAB next") {
		t.Fatalf("form view = %q", view)
	}
	if buttons := render.renderButtons(); !strings.Contains(buttons, "Save") || !strings.Contains(buttons, "Delete") {
		t.Fatalf("renderButtons = %q", buttons)
	}
	if inline := render.renderInlineFields(0, 2); !strings.Contains(inline, "A") || !strings.Contains(inline, "B") {
		t.Fatalf("renderInlineFields = %q", inline)
	}
	if field := render.renderField(3); !strings.Contains(field, "Yes") {
		t.Fatalf("renderField choice = %q", field)
	}
}

func TestInfoModelUpdateAndView(t *testing.T) {
	a := testUIApp()
	lines := []string{}
	for i := 0; i < 30; i++ {
		lines = append(lines, "line")
	}
	m := infoModel{app: a, lang: "en", title: "Info", lines: lines, actions: []option{{"b", "Back"}, {"d", "Delete"}}}
	model, _ := m.Update(key("pgdown"))
	m = model.(infoModel)
	if m.offset == 0 {
		t.Fatal("pgdown did not scroll")
	}
	model, _ = m.Update(key("pgup"))
	m = model.(infoModel)
	if m.offset != 0 {
		t.Fatalf("pgup offset = %d", m.offset)
	}
	model, _ = m.Update(key("tab"))
	m = model.(infoModel)
	if m.actionCursor != 1 {
		t.Fatalf("tab action cursor = %d", m.actionCursor)
	}
	model, _ = m.Update(key("enter"))
	m = model.(infoModel)
	if m.chosen != "d" {
		t.Fatalf("enter chosen = %q", m.chosen)
	}
	m = infoModel{app: a, lang: "en", title: "Info", lines: lines, actions: []option{{"b", "Back"}, {"d", "Delete"}}}
	model, _ = m.Update(runeKey('b'))
	m = model.(infoModel)
	if m.chosen != "b" {
		t.Fatalf("shortcut chosen = %q", m.chosen)
	}
	m = infoModel{app: a, lang: "en", title: "Info", lines: lines}
	model, _ = m.Update(key("esc"))
	m = model.(infoModel)
	if m.chosen != "q" {
		t.Fatalf("esc chosen = %q", m.chosen)
	}
	view := infoModel{app: a, lang: "en", title: "Info", lines: lines, actions: []option{{"b", "Back"}}}.View()
	if !strings.Contains(view, "Info") || !strings.Contains(view, "Back") || !strings.Contains(view, "Up/down") {
		t.Fatalf("info view = %q", view)
	}
	if (infoModel{lines: lines}).maxOffset() != len(lines)-1 {
		t.Fatal("maxOffset failed")
	}
}
