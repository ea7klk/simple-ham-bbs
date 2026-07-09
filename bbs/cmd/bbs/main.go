package main

import (
	"fmt"
	"os"
)

func main() {
	a, err := newApp()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	if err := a.seedData(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	if len(os.Args) > 1 && os.Args[1] == "aprs-supervisor" {
		if err := a.runAPRSSupervisor(); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "aprs-receiver" {
		if err := a.runAPRSReceiver(); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		return
	}
	defer fmt.Print("\033[?25h\033[?2004l")
	if err := a.run(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
