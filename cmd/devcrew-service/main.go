package main

import (
	"os"

	"github.com/comisai/comis-dev-crew/internal/command"
)

func main() {
	os.Exit(command.Run("devcrew-service", os.Args[1:], os.Stdout, os.Stderr))
}
