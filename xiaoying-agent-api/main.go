package main

import (
	"github.com/auramate/auramate/cmd"
	"github.com/auramate/auramate/internal/logging"
)

func main() {
	defer logging.RecoverPanic("main", func() {
		logging.ErrorPersist("Application terminated due to unhandled panic")
	})
	cmd.Execute()
}
