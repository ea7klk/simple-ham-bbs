package main

import (
	"crypto/tls"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"os"
	"strconv"
	"strings"
	"time"
)

// Outbound email.
//
// The BBS sends three kinds of mail, all of them transactional: the activation
// code at registration, a password-reset link, and the two links that confirm an
// email change. Everything is configured from the environment so no credential
// ever lives in the repository; .env.example carries Mailcow-shaped defaults.
//
// Sending is synchronous with a short timeout. A login is not the place to run a
// queue, and a hung SMTP conversation must not wedge a session.

const (
	mailDialTimeout  = 10 * time.Second
	mailTotalTimeout = 20 * time.Second
	// mailBodyWidth keeps message bodies readable in monospace and plain-text
	// clients without hard-wrapping URLs, which are appended on their own line.
	mailBodyWidth = 72
)

// mailConfig is captured at startup from the environment.
type mailConfig struct {
	host     string
	port     int
	username string
	password string
	from     string
	fromName string
	// security is "starttls", "tls" or "none".
	security string
	// webBaseURL is the public base URL of the web BBS. Tokenised links point
	// there, never at the SSH service.
	webBaseURL string
}

// mailer sends messages; nil-safe so an unconfigured deployment can call it.
type mailer struct {
	cfg   mailConfig
	now   func() time.Time
	send  func(msg mailMessage) error
	quiet bool
}

type mailMessage struct {
	To      string
	Subject string
	Body    string
}

func newMailerFromEnv() *mailer {
	port, _ := strconv.Atoi(env("BBS_SMTP_PORT", "587"))
	m := &mailer{cfg: mailConfig{
		host:       strings.TrimSpace(os.Getenv("BBS_SMTP_HOST")),
		port:       port,
		username:   strings.TrimSpace(os.Getenv("BBS_SMTP_USERNAME")),
		password:   os.Getenv("BBS_SMTP_PASSWORD"),
		from:       strings.TrimSpace(env("BBS_SMTP_FROM", os.Getenv("BBS_SMTP_USERNAME"))),
		fromName:   env("BBS_SMTP_FROM_NAME", "HAMNET Radio BBS"),
		security:   strings.ToLower(env("BBS_SMTP_SECURITY", "starttls")),
		webBaseURL: strings.TrimRight(env("BBS_WEB_BASE_URL", "https://bbs.ea7lk.es"), "/"),
	}, now: time.Now}
	m.send = m.smtpSend
	return m
}

// Enabled reports whether outbound mail is configured. Without a host there is
// nowhere to send to, so the account flows that depend on mail degrade to their
// pre-existing behaviour and say so instead of pretending to have sent a link.
func (m *mailer) Enabled() bool {
	return m != nil && m.cfg.host != "" && m.cfg.from != ""
}

func (m *mailer) WebURL(path string) string {
	base := "https://bbs.ea7lk.es"
	if m != nil && strings.TrimSpace(m.cfg.webBaseURL) != "" {
		base = m.cfg.webBaseURL
	}
	return strings.TrimRight(base, "/") + "/" + strings.TrimLeft(path, "/")
}

// Send delivers one message. A nil or unconfigured mailer is an error, because
// every caller is about to tell a user that mail was sent.
func (m *mailer) Send(msg mailMessage) error {
	if !m.Enabled() {
		return errors.New("mail is not configured")
	}
	msg.To = strings.TrimSpace(msg.To)
	if msg.To == "" {
		return errors.New("mail: empty recipient")
	}
	if m.send != nil {
		return m.send(msg)
	}
	return m.smtpSend(msg)
}

// smtpSend performs one SMTP transaction.
//
// "starttls" upgrades an ordinary connection (submission port 587, Mailcow's
// default); "tls" dials TLS directly (port 465); "none" is for a local relay.
// Authentication is attempted only when a username is configured, so an
// unauthenticated relay keeps working.
func (m *mailer) smtpSend(msg mailMessage) error {
	address := net.JoinHostPort(m.cfg.host, strconv.Itoa(m.cfg.port))
	dialer := &net.Dialer{Timeout: mailDialTimeout}
	deadline := m.now().Add(mailTotalTimeout)
	tlsConfig := &tls.Config{ServerName: m.cfg.host, MinVersion: tls.VersionTLS12}

	conn, err := dialer.Dial("tcp", address)
	if err != nil {
		return fmt.Errorf("mail: dial %s: %w", address, err)
	}
	// One deadline for the whole conversation: a server that accepts the
	// connection and then stalls must not hold a login open indefinitely.
	_ = conn.SetDeadline(deadline)
	if m.cfg.security == "tls" {
		tlsConn := tls.Client(conn, tlsConfig)
		if err := tlsConn.Handshake(); err != nil {
			_ = conn.Close()
			return fmt.Errorf("mail: tls handshake with %s: %w", address, err)
		}
		conn = tlsConn
	}

	client, err := smtp.NewClient(conn, m.cfg.host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("mail: greet %s: %w", address, err)
	}
	defer func() { _ = client.Close() }()

	if m.cfg.security == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return errors.New("mail: server does not offer STARTTLS; set BBS_SMTP_SECURITY=tls or none")
		}
		if err := client.StartTLS(tlsConfig); err != nil {
			return fmt.Errorf("mail: starttls: %w", err)
		}
	}
	if m.cfg.username != "" {
		if err := client.Auth(smtp.PlainAuth("", m.cfg.username, m.cfg.password, m.cfg.host)); err != nil {
			return fmt.Errorf("mail: auth as %s: %w", m.cfg.username, err)
		}
	}
	if err := client.Mail(m.cfg.from); err != nil {
		return fmt.Errorf("mail: MAIL FROM: %w", err)
	}
	if err := client.Rcpt(msg.To); err != nil {
		return fmt.Errorf("mail: RCPT TO %s: %w", msg.To, err)
	}
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("mail: DATA: %w", err)
	}
	if _, err := writer.Write([]byte(m.render(msg))); err != nil {
		_ = writer.Close()
		return fmt.Errorf("mail: write body: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("mail: finish body: %w", err)
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("mail: quit: %w", err)
	}
	return nil
}

// render builds the RFC 5322 message. Headers that may carry non-ASCII text are
// encoded; the body is UTF-8 and marked as such.
func (m *mailer) render(msg mailMessage) string {
	var b strings.Builder
	b.WriteString("From: " + formatAddress(m.cfg.fromName, m.cfg.from) + "\r\n")
	b.WriteString("To: " + formatAddress("", msg.To) + "\r\n")
	b.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", msg.Subject) + "\r\n")
	b.WriteString("Date: " + m.now().UTC().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("Message-ID: " + m.messageID(msg) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("Content-Transfer-Encoding: 8bit\r\n")
	b.WriteString("Auto-Submitted: auto-generated\r\n")
	b.WriteString("\r\n")
	// SMTP requires CRLF line endings and strips bare dots at line starts.
	for _, line := range strings.Split(strings.ReplaceAll(msg.Body, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(line, ".") {
			line = "." + line
		}
		b.WriteString(line + "\r\n")
	}
	return b.String()
}

func (m *mailer) messageID(msg mailMessage) string {
	domain := "bbs.local"
	if at := strings.LastIndex(m.cfg.from, "@"); at >= 0 && at+1 < len(m.cfg.from) {
		domain = m.cfg.from[at+1:]
	}
	return fmt.Sprintf("<%d.%s@%s>", m.now().UnixNano(), sanitizeMessageIDPart(msg.To), domain)
}

func sanitizeMessageIDPart(value string) string {
	var b strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "msg"
	}
	return b.String()
}

// formatAddress renders "Name <addr>", encoding a non-ASCII display name and
// quoting one that contains characters that would confuse a parser.
func formatAddress(name, address string) string {
	name = sanitizeSingleLineText(name)
	if name == "" {
		return "<" + address + ">"
	}
	if !isASCII(name) {
		return mime.QEncoding.Encode("utf-8", name) + " <" + address + ">"
	}
	if strings.ContainsAny(name, "()<>[]:;@\\,.\"") {
		return `"` + strings.ReplaceAll(name, `"`, `\"`) + `" <` + address + ">"
	}
	return name + " <" + address + ">"
}

func isASCII(value string) bool {
	for _, r := range value {
		if r > 127 {
			return false
		}
	}
	return true
}

// wrapBody breaks prose at spaces without touching long tokens, so a URL is
// never split across lines by the wrapper.
func wrapBody(text string, width int) string {
	if width < 20 {
		width = 20
	}
	var out []string
	for _, paragraph := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(paragraph) == "" {
			out = append(out, "")
			continue
		}
		line := ""
		for _, word := range strings.Fields(paragraph) {
			switch {
			case line == "":
				line = word
			case len([]rune(line))+1+len([]rune(word)) > width:
				out = append(out, line)
				line = word
			default:
				line += " " + word
			}
		}
		if line != "" {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}
