package main

import (
	"fmt"
	"os"
)

func main() {
	defer fmt.Print("\033[?25h\033[?2004l")
	a, err := newApp()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	if err := a.seedData(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	if err := a.run(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
