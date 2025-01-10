package main

import (
	"fmt"
	"os"

	"github.com/System233/enkit/enkit/cmd"
)

func main() {
	command, err := cmd.New()
	exitIf(err)

	command.Run()
}

func exitIf(err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}
