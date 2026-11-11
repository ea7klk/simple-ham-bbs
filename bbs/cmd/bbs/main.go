package main

import (
	"fmt"
	"os"
)

func main() {
	a, err := newApp()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	switch command() {
	case "migrate":
		// One-shot schema migration and seeding, for an operator or a deployment
		// that would rather run it explicitly than rely on the supervisor.
		if err := a.initialize(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("schema up to date")
		return
	case "aprs-supervisor":
		// The supervisor is the process that owns the one-shot startup work.
		if err := a.initialize(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := a.runAPRSSupervisor(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	case "aprs-receiver":
		if err := a.waitForSchema(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := a.runAPRSReceiver(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	// An interactive session. The email-change sweep runs in the supervisor's
	// startup, so a session only has to know the schema is there.
	if err := a.waitForSchema(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer fmt.Print("\033[?25h\033[?2004l")
	if err := a.run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// command returns the subcommand argument, or "" for an interactive session.
func command() string {
	if len(os.Args) < 2 {
		return ""
	}
	return os.Args[1]
}
