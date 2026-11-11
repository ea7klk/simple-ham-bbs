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
	// schemaWaitTimeout bounds how long a session waits for the supervisor's
	// one-shot migration before giving up with an explanation. The supervisor
	// starts first in the container, so in practice this wait is microseconds;
	// it exists so a missing migration is a clear message rather than a query
	// against a table that does not exist.
	schemaWaitTimeout      = 20 * time.Second
	schemaPollInterval     = 100 * time.Millisecond
	screenWidth            = 132
	screenHeight           = 24
	panelBorderWidth       = 2
	panelPaddingWidth      = 2
	panelVerticalFrame     = 2
	panelStyleWidth        = screenWidth - panelBorderWidth
	panelContentWidth      = panelStyleWidth - panelPaddingWidth
	panelContentHeight     = screenHeight - panelVerticalFrame
	formInputWidth         = 72
	formSingleLineMaxWidth = 72
	formTextAreaHeight     = 5
	menuOptionColumnWidth  = 3
)

// maxReplyDepth bounds how deep replies may nest below a top-level message, so
// a thread is at most maxReplyDepth+1 rows deep. Only top-level messages count
// toward the per-board message budget; replies are bounded by this depth.
const maxReplyDepth = 5

// schemaWait bounds the wait in waitForSchema.
var schemaWait = 20 * time.Second

var (
	callsignRE     = regexp.MustCompile(`^[A-Z0-9][A-Z0-9/-]{2,15}$`)
	aprsCallsignRE = regexp.MustCompile(`^[A-Z0-9]{1,10}(-[0-9]{1,2})?$`)
	emailRE        = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
	maidenheadRE   = regexp.MustCompile(`^[A-Ra-r]{2}([0-9]{2}([A-Xa-x]{2}([0-9]{2}([A-Xa-x]{2})?)?)?)?$`)
	boardIDRE      = regexp.MustCompile(`[^a-z0-9]+`)
	boxBorder      = lipgloss.Border{Top: "─", Bottom: "─", Left: "│", Right: "│", TopLeft: "┌", TopRight: "┐", BottomLeft: "└", BottomRight: "┘"}
	titleStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("14"))
	subtitleStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	selectedStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("0")).Background(lipgloss.Color("14"))
	dimStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	errorStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)
	successStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true)
	cursorStyle    = lipgloss.NewStyle().Reverse(true)
	panelStyle     = lipgloss.NewStyle().Width(panelStyleWidth).Height(panelContentHeight).Border(boxBorder).BorderForeground(lipgloss.Color("14")).Padding(0, 1)
	formPanelStyle = lipgloss.NewStyle().Width(panelStyleWidth).Height(panelContentHeight).Border(boxBorder).BorderForeground(lipgloss.Color("10")).Padding(0, 1)
	languages      = map[string]string{"en": "English", "es": "Espanol", "fr": "Francais", "de": "Deutsch"}
	languageOrder  = []string{"en", "es", "fr", "de"}
)

type config struct {
	dataDir              string
	dbFile               string
	aprsLogFile          string
	bbsLogFile           string
	authLogFile          string
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
	currentUserIsSysop  bool
	terminalError       error
	mail                *mailer
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
	// Registered is when the account was created. It is written once and never
	// changes, unlike FirstSeen/LastSeen which track presence.
	Registered string `json:"registered,omitempty"`
	// Activated is false until the caller proves they can read the address they
	// registered with. Accounts that predate activation are backfilled to true.
	Activated bool `json:"activated,omitempty"`
}

type bulletin struct {
	ID      uint   `json:"id,omitempty"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	Updated string `json:"updated"`
	From    string `json:"from,omitempty"`
}

type message struct {
	ID      uint   `json:"id,omitempty"`
	From    string `json:"from"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
	Created string `json:"created"`
	Edited  string `json:"edited,omitempty"`
	// Depth is 0 for a top-level message and increases by one per reply level.
	// It is derived when loading, not stored.
	Depth   int       `json:"-"`
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
