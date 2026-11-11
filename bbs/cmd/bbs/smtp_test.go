package main

import (
	"bufio"
	"net"
	"strings"
	"testing"
	"time"
)

// TestSMTPTransactionAgainstFakeServer exercises the real client code path over
// a socket: greeting, envelope, DATA and QUIT, with security "none" so no TLS is
// needed in the test. It proves the message that goes on the wire is the one
// render() built.
func TestSMTPTransactionAgainstFakeServer(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	received := make(chan string, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		write := func(line string) { _, _ = conn.Write([]byte(line + "\r\n")) }

		write("220 fake ESMTP")
		var data strings.Builder
		inData := false
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if inData {
				if line == "." {
					inData = false
					write("250 queued")
					continue
				}
				data.WriteString(line + "\n")
				continue
			}
			switch {
			case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
				write("250-fake")
				write("250 SIZE 10240000")
			case strings.HasPrefix(line, "MAIL FROM"):
				write("250 ok")
			case strings.HasPrefix(line, "RCPT TO"):
				write("250 ok")
			case line == "DATA":
				inData = true
				write("354 send it")
			case line == "QUIT":
				write("221 bye")
				received <- data.String()
				return
			default:
				write("250 ok")
			}
		}
	}()

	port := listener.Addr().(*net.TCPAddr).Port
	m := &mailer{
		cfg: mailConfig{
			host:     "127.0.0.1",
			port:     port,
			from:     "bbs@example.org",
			fromName: "Test BBS",
			security: "none",
		},
		now: func() time.Time { return time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC) },
	}
	if err := m.Send(mailMessage{
		To:      "user@example.org",
		Subject: "Activation",
		Body:    "Your code is ABCD",
	}); err != nil {
		t.Fatalf("Send() = %v", err)
	}

	select {
	case body := <-received:
		for _, want := range []string{"Subject: Activation", "Your code is ABCD", "To: <user@example.org>"} {
			if !strings.Contains(body, want) {
				t.Fatalf("wire message missing %q:\n%s", want, body)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("fake server never received the message")
	}
}

// A server that refuses the recipient must produce an error, not a silent drop.
func TestSMTPRecipientRefused(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		_, _ = conn.Write([]byte("220 fake ESMTP\r\n"))
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			switch {
			case strings.HasPrefix(line, "RCPT TO"):
				_, _ = conn.Write([]byte("550 no such user\r\n"))
			case strings.HasPrefix(line, "QUIT"):
				_, _ = conn.Write([]byte("221 bye\r\n"))
				return
			default:
				_, _ = conn.Write([]byte("250 ok\r\n"))
			}
		}
	}()

	port := listener.Addr().(*net.TCPAddr).Port
	m := &mailer{cfg: mailConfig{host: "127.0.0.1", port: port, from: "bbs@example.org", security: "none"}, now: time.Now}
	err = m.Send(mailMessage{To: "refused@example.org", Subject: "s", Body: "b"})
	if err == nil || !strings.Contains(err.Error(), "RCPT TO") {
		t.Fatalf("Send() error = %v, want a recipient rejection", err)
	}
}
