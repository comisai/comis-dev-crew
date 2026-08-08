package main

import (
	"os"

	"github.com/comisai/comis-dev-crew/internal/command"
)

func main() {
	os.Exit(command.Run("devcrew-report", os.Args[1:], os.Stdout, os.Stderr))
}
