package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type menuModel struct {
	app     *app
	lang    string
	title   string
	header  string
	options []option
	cursor  int
	chosen  string
	done    bool
}

func (m menuModel) Init() tea.Cmd { return nil }

func (m menuModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			m.chosen, m.done = "q", true
			return m, tea.Quit
		case "up", "k", "shift+tab":
			if m.cursor > 0 {
				m.cursor--
			} else {
				m.cursor = len(m.options) - 1
			}
		case "down", "j", "tab":
			m.cursor = (m.cursor + 1) % len(m.options)
		case "pgup":
			m.cursor -= m.pageStep()
			if m.cursor < 0 {
				m.cursor = 0
			}
		case "pgdown":
			m.cursor += m.pageStep()
			if m.cursor >= len(m.options) {
				m.cursor = len(m.options) - 1
			}
		case "enter":
			m.chosen, m.done = m.options[m.cursor].value, true
			return m, tea.Quit
		default:
			key := strings.ToLower(msg.String())
			for i, opt := range m.options {
				if strings.ToLower(opt.value) == key {
					m.cursor, m.chosen, m.done = i, opt.value, true
					return m, tea.Quit
				}
			}
		}
	}
	return m, nil
}

func (m menuModel) View() string {
	var b strings.Builder
	b.WriteString(m.app.banner(m.lang))
	if m.header != "" {
		b.WriteString(limitLines(m.header, 5) + "\n\n")
	}
	b.WriteString(titleStyle.Render(m.title) + "\n\n")
	prefixLines := lineCount(b.String())
	optionRoom := menuOptionRoom(prefixLines)
	start, end := visibleRange(len(m.options), m.cursor, optionRoom)
	if start > 0 {
		b.WriteString(dimStyle.Render("...") + "\n")
	}
	for i := start; i < end; i++ {
		opt := m.options[i]
		b.WriteString(renderMenuOption(opt, i == m.cursor) + "\n")
	}
	if end < len(m.options) {
		b.WriteString(dimStyle.Render("...") + "\n")
	}
	b.WriteString("\n" + dimWrapped(m.app.t(m.lang, "menu_hint_actions"), panelContentWidth))
	return panelStyle.Render(b.String())
}

func renderMenuOption(opt option, selected bool) string {
	row := fmt.Sprintf(" %*s  %s", menuOptionColumnWidth, opt.value, opt.label)
	if selected {
		return selectedStyle.Render(">" + row)
	}
	return " " + row
}

func menuOptionRoom(prefixLines int) int {
	room := panelContentHeight - prefixLines - 2
	if room < 3 {
		return 3
	}
	return room - 2
}

func (m menuModel) pageStep() int {
	var b strings.Builder
	b.WriteString(m.app.banner(m.lang))
	if m.header != "" {
		b.WriteString(limitLines(m.header, 5) + "\n\n")
	}
	b.WriteString(titleStyle.Render(m.title) + "\n\n")
	return max(1, menuOptionRoom(lineCount(b.String()))-2)
}

func visibleRange(total, cursor, room int) (int, int) {
	if total <= room {
		return 0, total
	}
	usable := room
	if cursor > 0 {
		usable--
	}
	if cursor < total-1 {
		usable--
	}
	if usable < 1 {
		usable = 1
	}
	start := cursor - usable/2
	if start < 0 {
		start = 0
	}
	end := start + usable
	if end > total {
		end = total
		start = end - usable
		if start < 0 {
			start = 0
		}
	}
	return start, end
}

func lineCount(text string) int {
	if text == "" {
		return 0
	}
	return strings.Count(strings.TrimRight(text, "\n"), "\n") + 1
}

func limitLines(text string, maxLines int) string {
	if maxLines <= 0 {
		return ""
	}
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) <= maxLines {
		return strings.Join(lines, "\n")
	}
	out := append([]string{}, lines[:maxLines]...)
	out[maxLines-1] = dimStyle.Render("...")
	return strings.Join(out, "\n")
}

func dimWrapped(text string, width int) string {
	lines := wrapText(text, width)
	for i := range lines {
		lines[i] = dimStyle.Render(lines[i])
	}
	return strings.Join(lines, "\n")
}

func wrapText(text string, width int) []string {
	if width < 10 {
		width = 10
	}
	var out []string
	for _, raw := range strings.Split(text, "\n") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			out = append(out, "")
			continue
		}
		words := strings.Fields(raw)
		line := ""
		for _, word := range words {
			if line == "" {
				line = word
				continue
			}
			if lipgloss.Width(line)+1+lipgloss.Width(word) > width {
				out = append(out, line)
				line = word
				continue
			}
			line += " " + word
		}
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func truncateText(text string, width int) string {
	text = strings.Join(strings.Fields(text), " ")
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(text) <= width {
		return text
	}
	suffix := "..."
	suffixWidth := lipgloss.Width(suffix)
	if width <= suffixWidth {
		return truncateDisplayWidth(text, width)
	}
	return truncateDisplayWidth(text, width-suffixWidth) + suffix
}

func truncateDisplayWidth(text string, width int) string {
	return ansi.Truncate(text, width, "")
}

type fieldKind int

const (
	fieldText fieldKind = iota
	fieldPassword
	fieldTextArea
	fieldChoice
)

type formField struct {
	name        string
	label       string
	kind        fieldKind
	value       string
	required    bool
	limit       int
	width       int
	sameLine    bool
	choices     []option
	input       textinput.Model
	area        textarea.Model
	validator   func(string) bool
	normalizer  func(string) string
	invalidText string
}

type formModel struct {
	app       *app
	lang      string
	title     string
	fields    []formField
	buttons   []string
	noButtons bool
	focus     int
	err       string
	action    string
	values    map[string]string
	done      bool
}

func newFormModel(a *app, lang, title string, fields []formField, buttons []string) formModel {
	noButtons := buttons == nil
	if noButtons {
		buttons = []string{}
	} else if len(buttons) == 0 {
		buttons = []string{"save", "cancel"}
	}
	for i := range fields {
		switch fields[i].kind {
		case fieldText, fieldPassword:
			ti := textinput.New()
			ti.SetValue(fields[i].value)
			ti.CharLimit = fields[i].limit
			ti.Width = formInputWidth
			ti.Prompt = ""
			if fields[i].kind == fieldPassword {
				ti.EchoMode = textinput.EchoPassword
				ti.EchoCharacter = '*'
			}
			fields[i].input = ti
		case fieldTextArea:
			ta := textarea.New()
			ta.SetValue(fields[i].value)
			ta.CharLimit = fields[i].limit
			ta.SetWidth(panelContentWidth - 4)
			ta.SetHeight(formTextAreaHeight)
			ta.ShowLineNumbers = false
			fields[i].area = ta
		}
	}
	m := formModel{app: a, lang: lang, title: title, fields: fields, buttons: buttons, noButtons: noButtons}
	m.setFocus(0)
	return m
}

func (m *formModel) setFocus(f int) {
	total := len(m.fields) + len(m.buttons)
	if total == 0 {
		return
	}
	m.focus = ((f % total) + total) % total
	for i := range m.fields {
		if i == m.focus {
			if m.fields[i].kind == fieldText || m.fields[i].kind == fieldPassword {
				m.fields[i].input.Focus()
			}
			if m.fields[i].kind == fieldTextArea {
				m.fields[i].area.Focus()
			}
		} else {
			m.fields[i].input.Blur()
			m.fields[i].area.Blur()
		}
	}
}

func (m formModel) Init() tea.Cmd { return textinput.Blink }

func (m formModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		m.err = ""
		if msg.String() == "ctrl+c" {
			m.action, m.done = "cancel", true
			return m, tea.Quit
		}
		if msg.String() == "esc" {
			m.action, m.done = "cancel", true
			return m, tea.Quit
		}
		if msg.String() == "tab" {
			m.setFocus(m.focus + 1)
			return m, nil
		}
		if msg.String() == "shift+tab" {
			m.setFocus(m.focus - 1)
			return m, nil
		}
		if m.focus >= len(m.fields) {
			if msg.String() == "left" || msg.String() == "up" {
				m.setFocus(m.focus - 1)
				return m, nil
			}
			if msg.String() == "right" || msg.String() == "down" {
				m.setFocus(m.focus + 1)
				return m, nil
			}
			if msg.String() == "enter" {
				button := m.buttons[m.focus-len(m.fields)]
				if button == "cancel" || button == "delete" {
					m.action, m.done = button, true
					return m, tea.Quit
				}
				values, err := m.validate()
				if err != "" {
					m.err = err
					return m, nil
				}
				m.action, m.values, m.done = button, values, true
				return m, tea.Quit
			}
			return m, nil
		}
		field := &m.fields[m.focus]
		if field.kind == fieldChoice {
			if msg.String() == "left" || msg.String() == "up" {
				field.value = choiceStep(field.choices, field.value, -1)
			} else if msg.String() == "right" || msg.String() == "down" || msg.String() == " " || msg.String() == "enter" {
				field.value = choiceStep(field.choices, field.value, 1)
			}
			return m, nil
		}
		if msg.String() == "enter" && field.kind != fieldTextArea {
			if m.noButtons && m.focus == len(m.fields)-1 {
				values, err := m.validate()
				if err != "" {
					m.err = err
					return m, nil
				}
				m.action, m.values, m.done = "submit", values, true
				return m, tea.Quit
			}
			m.setFocus(m.focus + 1)
			return m, nil
		}
		var cmd tea.Cmd
		if field.kind == fieldText || field.kind == fieldPassword {
			field.input, cmd = field.input.Update(msg)
		}
		if field.kind == fieldTextArea {
			field.area, cmd = field.area.Update(msg)
		}
		return m, cmd
	}
	return m, nil
}

func (m formModel) validate() (map[string]string, string) {
	values := map[string]string{}
	for i := range m.fields {
		f := &m.fields[i]
		value := f.value
		switch f.kind {
		case fieldText, fieldPassword:
			value = f.input.Value()
		case fieldTextArea:
			value = f.area.Value()
		}
		value = strings.TrimSpace(value)
		if f.normalizer != nil {
			value = f.normalizer(value)
		}
		if f.limit > 0 && len(value) > f.limit {
			value = value[:f.limit]
		}
		if f.required && value == "" {
			return nil, fmt.Sprintf("%s: %s", f.label, m.app.t(m.lang, "required"))
		}
		if value != "" && f.validator != nil && !f.validator(value) {
			if f.invalidText != "" {
				return nil, f.invalidText
			}
			return nil, m.app.t(m.lang, "invalid_choice")
		}
		values[f.name] = value
	}
	if values["new_password"] != "" || values["verify_password"] != "" {
		if len(values["new_password"]) < 8 {
			return nil, m.app.t(m.lang, "password_short")
		}
		if values["new_password"] != values["verify_password"] {
			return nil, m.app.t(m.lang, "password_mismatch")
		}
	}
	return values, ""
}

func (m formModel) View() string {
	var b strings.Builder
	b.WriteString(m.app.banner(m.lang))
	b.WriteString(limitLines(titleStyle.Render(m.title), 6) + "\n\n")

	buttonsLine := m.renderButtons()
	errLines := 0
	if m.err != "" {
		errLines = 2
	}
	fixedLines := lineCount(b.String()) + 1 + 1 + errLines
	fieldRoom := panelContentHeight - fixedLines
	if fieldRoom < 4 {
		fieldRoom = 4
	} else {
		fieldRoom -= 2
	}
	start, end := m.visibleFieldRange(fieldRoom)
	if start > 0 {
		b.WriteString(dimStyle.Render(m.app.t(m.lang, "form_more_fields_above")) + "\n")
	}
	for i := start; i < end; i++ {
		if m.fields[i].sameLine {
			groupEnd := i + 1
			for groupEnd < end && m.fields[groupEnd].sameLine {
				groupEnd++
			}
			b.WriteString(m.renderInlineFields(i, groupEnd))
			i = groupEnd - 1
			continue
		}
		b.WriteString(m.renderField(i))
	}
	if end < len(m.fields) {
		b.WriteString(dimStyle.Render(m.app.t(m.lang, "form_more_fields_below")) + "\n")
	}
	b.WriteString(buttonsLine)
	if m.err != "" {
		b.WriteString("\n\n" + errorStyle.Render(m.err))
	}
	hintKey := "form_hint_actions"
	if m.noButtons {
		hintKey = "form_hint_submit"
	}
	b.WriteString("\n" + dimWrapped(m.app.t(m.lang, hintKey), panelContentWidth))
	return formPanelStyle.Render(b.String())
}

func (m formModel) renderButtons() string {
	if len(m.buttons) == 0 {
		return ""
	}
	var b strings.Builder
	for i, button := range m.buttons {
		label := m.app.t(m.lang, buttonLabelKey(button))
		text := "[ " + label + " ]"
		if len(m.fields)+i == m.focus {
			text = selectedStyle.Render(text)
		} else {
			text = titleStyle.Render(text)
		}
		b.WriteString(text + "  ")
	}
	return b.String()
}

func (m formModel) renderField(i int) string {
	var b strings.Builder
	f := &m.fields[i]
	labelText := strings.TrimSpace(f.label)
	if f.required {
		labelText += " *"
	}
	label := labelText
	if i == m.focus {
		label = selectedStyle.Render(label)
	} else {
		label = titleStyle.Render(label)
	}
	switch f.kind {
	case fieldText, fieldPassword:
		fieldWidth := panelContentWidth - lipgloss.Width(labelText) - 10
		if f.width > 0 {
			fieldWidth = f.width
		}
		if fieldWidth > formSingleLineMaxWidth {
			fieldWidth = formSingleLineMaxWidth
		}
		if fieldWidth < 12 {
			fieldWidth = 12
		}
		b.WriteString(label + " " + m.renderSingleLineField(f, i == m.focus, fieldWidth) + "\n")
	case fieldTextArea:
		b.WriteString(label + "\n")
		b.WriteString(f.area.View() + "\n")
	case fieldChoice:
		b.WriteString(label + "\n")
		for _, choice := range f.choices {
			text := choice.label
			if choice.value == f.value {
				text = selectedStyle.Render(" " + text + " ")
			}
			b.WriteString(text + " ")
		}
		b.WriteString("\n")
	}
	return b.String()
}

func (m formModel) renderInlineFields(start, end int) string {
	var b strings.Builder
	for i := start; i < end; i++ {
		f := &m.fields[i]
		labelText := strings.TrimSpace(f.label)
		if f.required {
			labelText += " *"
		}
		label := labelText
		if i == m.focus {
			label = selectedStyle.Render(label)
		} else {
			label = titleStyle.Render(label)
		}
		fieldWidth := f.width
		if fieldWidth <= 0 {
			fieldWidth = 12
		}
		b.WriteString(label + " " + m.renderSingleLineField(f, i == m.focus, fieldWidth))
		if i < end-1 {
			b.WriteString("  ")
		}
	}
	b.WriteString("\n")
	return b.String()
}

func (m formModel) renderSingleLineField(f *formField, focused bool, width int) string {
	display := f.input.Value()
	if f.kind == fieldPassword {
		display = strings.Repeat(string(f.input.EchoCharacter), len([]rune(display)))
	}
	if width <= 0 {
		width = panelContentWidth - 2
	}
	runes := []rune(display)
	pos := f.input.Position()
	if pos < 0 {
		pos = 0
	}
	if pos > len(runes) {
		pos = len(runes)
	}
	start := 0
	if pos >= width {
		start = pos - width + 1
	}
	end := start + width
	if end > len(runes) {
		end = len(runes)
	}
	visible := append([]rune{}, runes[start:end]...)
	if focused {
		cursorPos := pos - start
		if cursorPos < 0 {
			cursorPos = 0
		}
		if cursorPos >= width {
			cursorPos = width - 1
		}
		for len(visible) <= cursorPos {
			visible = append(visible, ' ')
		}
		left := string(visible[:cursorPos])
		cell := string(visible[cursorPos])
		right := ""
		if cursorPos+1 < len(visible) {
			right = string(visible[cursorPos+1:])
		}
		line := left + cursorStyle.Render(cell) + right
		return "[" + line + strings.Repeat(" ", width-len(visible)) + "]"
	}
	line := string(visible)
	return "[" + line + strings.Repeat(" ", width-len(visible)) + "]"
}

func (m formModel) visibleFieldRange(room int) (int, int) {
	if len(m.fields) == 0 {
		return 0, 0
	}
	focus := m.focus
	if focus >= len(m.fields) {
		focus = len(m.fields) - 1
	}
	start, end, used := focus, focus+1, m.fieldLines(focus)
	for start > 0 && used+m.fieldLines(start-1) <= room {
		start--
		used += m.fieldLines(start)
	}
	for end < len(m.fields) && used+m.fieldLines(end) <= room {
		used += m.fieldLines(end)
		end++
	}
	for used > room && end-start > 1 {
		if focus-start > end-1-focus {
			used -= m.fieldLines(start)
			start++
		} else {
			end--
			used -= m.fieldLines(end)
		}
	}
	return start, end
}

func (m formModel) fieldLines(i int) int {
	if i < 0 || i >= len(m.fields) {
		return 0
	}
	if m.fields[i].kind == fieldTextArea {
		return formTextAreaHeight + 1
	}
	if m.fields[i].kind == fieldText || m.fields[i].kind == fieldPassword {
		return 1
	}
	return 2
}

func (a *app) runMenu(lang, title, header string, opts []option) string {
	return a.runMenuWithCursor(lang, title, header, opts, 0)
}

func (a *app) runMenuWithCursor(lang, title, header string, opts []option, cursor int) string {
	if a.runMenuHook != nil {
		return a.runMenuHook(lang, title, header, opts)
	}
	clearScreen()
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= len(opts) && len(opts) > 0 {
		cursor = len(opts) - 1
	}
	m := menuModel{app: a, lang: lang, title: title, header: header, options: opts, cursor: cursor}
	model, err := newTerminalProgram(m).Run()
	if err != nil {
		return "q"
	}
	return model.(menuModel).chosen
}

func (a *app) runForm(lang, title string, fields []formField, buttons []string) (string, map[string]string, bool) {
	if a.runFormHook != nil {
		return a.runFormHook(lang, title, fields, buttons)
	}
	clearScreen()
	m := newFormModel(a, lang, title, fields, buttons)
	model, err := newTerminalProgram(m).Run()
	if err != nil {
		return "cancel", nil, false
	}
	done := model.(formModel)
	return done.action, done.values, done.action != "cancel" && done.values != nil
}

func clearScreen() {
	resizeClientTerminal()
	fmt.Print("\033[2J\033[3J\033[H")
}

func clientTerminalResizeSequence() string {
	return fmt.Sprintf("\033[8;%d;%dt", screenHeight, screenWidth)
}

func resizeClientTerminal() {
	// This is supported by common xterm-compatible SSH clients. Clients that
	// do not allow application-driven resizing simply ignore the sequence.
	_, _ = fmt.Fprint(os.Stdout, clientTerminalResizeSequence())
}

func fixedTerminalSizeFilter(_ tea.Model, msg tea.Msg) tea.Msg {
	if _, ok := msg.(tea.WindowSizeMsg); ok {
		return tea.WindowSizeMsg{Width: screenWidth, Height: screenHeight}
	}
	return msg
}

func newTerminalProgram(model tea.Model) *tea.Program {
	return tea.NewProgram(
		model,
		tea.WithInput(os.Stdin),
		tea.WithOutput(os.Stdout),
		tea.WithAltScreen(),
		tea.WithFilter(fixedTerminalSizeFilter),
	)
}

type infoModel struct {
	app          *app
	lang         string
	title        string
	lines        []string
	actions      []option
	actionCursor int
	offset       int
	chosen       string
}

func (m infoModel) Init() tea.Cmd { return nil }

func (m infoModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc", "q":
			m.chosen = "q"
			return m, tea.Quit
		case "enter":
			if len(m.actions) > 0 {
				if m.actionCursor < 0 || m.actionCursor >= len(m.actions) {
					m.actionCursor = 0
				}
				m.chosen = m.actions[m.actionCursor].value
			} else {
				m.chosen = "q"
			}
			return m, tea.Quit
		case "tab", "right", "l":
			if len(m.actions) > 0 {
				m.actionCursor = (m.actionCursor + 1) % len(m.actions)
			}
		case "shift+tab", "left", "h":
			if len(m.actions) > 0 {
				m.actionCursor = (m.actionCursor - 1 + len(m.actions)) % len(m.actions)
			}
		case "up", "k":
			if m.offset > 0 {
				m.offset--
			}
		case "down", "j":
			if m.offset < m.maxOffset() {
				m.offset++
			}
		case "pgup":
			m.offset -= 10
			if m.offset < 0 {
				m.offset = 0
			}
		case "pgdown", " ":
			m.offset += 10
			if m.offset > m.maxOffset() {
				m.offset = m.maxOffset()
			}
		default:
			key := strings.ToLower(msg.String())
			for _, action := range m.actions {
				if strings.ToLower(action.value) == key {
					m.chosen = action.value
					return m, tea.Quit
				}
			}
		}
	}
	return m, nil
}

func (m infoModel) View() string {
	var b strings.Builder
	b.WriteString(m.app.banner(m.lang))
	b.WriteString(titleStyle.Render(m.title) + "\n\n")
	prefixLines := lineCount(b.String())
	room := panelContentHeight - prefixLines - 2
	if room < 4 {
		room = 4
	} else {
		room -= 2
	}
	end := m.offset + room
	if end > len(m.lines) {
		end = len(m.lines)
	}
	if m.offset > 0 {
		b.WriteString(dimStyle.Render(m.app.t(m.lang, "info_more_above")) + "\n")
	}
	for _, line := range m.lines[m.offset:end] {
		b.WriteString(line + "\n")
	}
	if end < len(m.lines) {
		b.WriteString(dimStyle.Render(m.app.t(m.lang, "info_more_below")) + "\n")
	}
	if len(m.actions) > 0 {
		b.WriteString("\n" + m.renderActions() + "\n")
	}
	hint := m.app.t(m.lang, "info_hint_actions")
	if len(m.actions) > 0 {
		parts := []string{}
		for _, action := range m.actions {
			parts = append(parts, action.value+" "+action.label)
		}
		hint += "  " + strings.Join(parts, "  ")
	}
	b.WriteString("\n" + dimWrapped(hint, panelContentWidth))
	return panelStyle.Render(b.String())
}

func (m infoModel) renderActions() string {
	var b strings.Builder
	for i, action := range m.actions {
		text := "[ " + action.label + " ]"
		if i == m.actionCursor {
			text = selectedStyle.Render(text)
		} else {
			text = titleStyle.Render(text)
		}
		b.WriteString(text + "  ")
	}
	return b.String()
}

func (m infoModel) maxOffset() int {
	return max(0, len(m.lines)-1)
}

func (a *app) showInfo(lang, title string, rows [][]string) {
	if a.showInfoHook != nil {
		a.showInfoHook(lang, title, rows)
		return
	}
	_ = a.showInfoActions(lang, title, rows, nil)
}

func (a *app) showInfoActions(lang, title string, rows [][]string, actions []option) string {
	if a.showInfoActionsHook != nil {
		return a.showInfoActionsHook(lang, title, rows, actions)
	}
	clearScreen()
	lines := []string{}
	for _, row := range rows {
		if len(row) == 1 {
			lines = append(lines, wrapText(row[0], panelContentWidth)...)
		} else if len(row) >= 2 {
			prefix := titleStyle.Render(row[0]) + ": "
			firstWidth := panelContentWidth - lipgloss.Width(row[0]) - 2
			if firstWidth < 10 {
				firstWidth = 10
			}
			wrapped := wrapText(row[1], firstWidth)
			if len(wrapped) == 0 {
				lines = append(lines, prefix)
				continue
			}
			lines = append(lines, prefix+wrapped[0])
			for _, paragraph := range wrapped[1:] {
				for _, line := range wrapText(paragraph, panelContentWidth-2) {
					lines = append(lines, "  "+line)
				}
			}
		}
	}
	model := infoModel{app: a, lang: lang, title: title, lines: lines, actions: actions}
	done, err := newTerminalProgram(model).Run()
	if err != nil {
		return "q"
	}
	if chosen := done.(infoModel).chosen; chosen != "" {
		return chosen
	}
	return "q"
}
