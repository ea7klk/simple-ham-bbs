package main

import (
	"github.com/charmbracelet/lipgloss"
	"gorm.io/gorm"
	"regexp"
	"time"
)

const (
	defaultBoardID              = "general"
	passwordIterations          = 200000
	aprsMessageLimit            = 67
	sentHistoryLimit            = 200
	receivedHistoryLimit        = 500
	aprsReceiverRestartInterval = time.Hour
	screenWidth                 = 132
	screenHeight                = 24
	panelBorderWidth            = 2
	panelPaddingWidth           = 2
	panelVerticalFrame          = 2
	panelStyleWidth             = screenWidth - panelBorderWidth
	panelContentWidth           = panelStyleWidth - panelPaddingWidth
	panelContentHeight          = screenHeight - panelVerticalFrame
	formInputWidth              = 72
	formSingleLineMaxWidth      = 72
	formTextAreaHeight          = 5
	menuOptionColumnWidth       = 3
)

var (
	callsignRE     = regexp.MustCompile(`^[A-Z0-9][A-Z0-9/-]{2,15}$`)
	aprsCallsignRE = regexp.MustCompile(`^[A-Z0-9]{1,10}(-[0-9]{1,2})?$`)
	emailRE        = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
	maidenheadRE   = regexp.MustCompile(`^[A-Ra-r]{2}([0-9]{2}([A-Xa-x]{2}([0-9]{2}([A-Xa-x]{2})?)?)?)?$`)
	boardIDRE      = regexp.MustCompile(`[^a-z0-9]+`)
	asciiBorder    = lipgloss.Border{Top: "-", Bottom: "-", Left: "|", Right: "|", TopLeft: "+", TopRight: "+", BottomLeft: "+", BottomRight: "+"}
	titleStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("14"))
	subtitleStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	selectedStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(lipgloss.Color("14"))
	dimStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	errorStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)
	successStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true)
	cursorStyle    = lipgloss.NewStyle().Reverse(true)
	panelStyle     = lipgloss.NewStyle().Width(panelStyleWidth).Height(panelContentHeight).Border(asciiBorder).BorderForeground(lipgloss.Color("14")).Padding(0, 1)
	formPanelStyle = lipgloss.NewStyle().Width(panelStyleWidth).Height(panelContentHeight).Border(asciiBorder).BorderForeground(lipgloss.Color("10")).Padding(0, 1)
	languages      = map[string]string{"en": "English", "es": "Espanol", "fr": "Francais", "de": "Deutsch"}
	languageOrder  = []string{"en", "es", "fr", "de"}
)

type config struct {
	dataDir              string
	dbFile               string
	aprsLogFile          string
	bbsLogFile           string
	authLogFile          string
	fail2banLogFile      string
	transFile            string
	name                 string
	sysopName            string
	sysops               map[string]bool
	location             string
	topic                string
	aprsServer           string
	aprsPort             int
	aprsReceiverCallsign string
}

type app struct {
	cfg                 config
	text                map[string]map[string]any
	db                  *gorm.DB
	currentUser         string
	runMenuHook         func(lang, title, header string, opts []option) string
	runFormHook         func(lang, title string, fields []formField, buttons []string) (string, map[string]string, bool)
	showInfoHook        func(lang, title string, rows [][]string)
	showInfoActionsHook func(lang, title string, rows [][]string, actions []option) string
	confirmDeleteHook   func(lang, prompt string) bool
}

type userProfile struct {
	FullName     string `json:"full_name,omitempty"`
	Email        string `json:"email,omitempty"`
	Maidenhead   string `json:"maidenhead,omitempty"`
	Language     string `json:"language,omitempty"`
	EnableAPRS   bool   `json:"enable_aprs,omitempty"`
	QTH          string `json:"qth,omitempty"`
	Rig          string `json:"rig,omitempty"`
	PasswordHash string `json:"password_hash,omitempty"`
	IsSysop      bool   `json:"is_sysop,omitempty"`
	Disabled     bool   `json:"disabled,omitempty"`
	FirstSeen    string `json:"first_seen,omitempty"`
	LastSeen     string `json:"last_seen,omitempty"`
}

type bulletin struct {
	Title   string `json:"title"`
	Body    string `json:"body"`
	Updated string `json:"updated"`
	From    string `json:"from,omitempty"`
}

type message struct {
	From    string    `json:"from"`
	Subject string    `json:"subject"`
	Body    string    `json:"body"`
	Created string    `json:"created"`
	Edited  string    `json:"edited,omitempty"`
	Replies []message `json:"replies,omitempty"`
}

type board struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Created     string    `json:"created"`
	Messages    []message `json:"messages"`
}

type boardsData struct {
	Boards []board `json:"boards"`
}

type sentAPRS struct {
	ID       uint           `json:"id,omitempty"`
	At       string         `json:"at"`
	From     string         `json:"from"`
	To       string         `json:"to"`
	Text     string         `json:"text"`
	Status   string         `json:"status"`
	Acked    bool           `json:"acked,omitempty"`
	Passcode int            `json:"passcode,omitempty"`
	Parts    []sentAPRSPart `json:"parts,omitempty"`
}

type sentAPRSPart struct {
	Number    int    `json:"number"`
	Text      string `json:"text"`
	Status    string `json:"status"`
	Detail    string `json:"detail,omitempty"`
	MessageID string `json:"message_id,omitempty"`
	Acked     bool   `json:"acked,omitempty"`
}

type receivedAPRS struct {
	ID        uint   `json:"id,omitempty"`
	At        string `json:"at"`
	From      string `json:"from"`
	To        string `json:"to"`
	Text      string `json:"text"`
	Raw       string `json:"raw,omitempty"`
	MessageID string `json:"message_id,omitempty"`
}

type option struct {
	value string
	label string
}
