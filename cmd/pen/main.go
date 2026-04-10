package main

import (
	"os"

	"github.com/agarcher/pen/internal/commands"
)

func main() {
	if err := commands.Execute(); err != nil {
		os.Exit(1)
	}
}
