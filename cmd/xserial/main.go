package main

import (
	"os"

	"github.com/ZhiWei-Ou/xserial/internal/cmd"
	"github.com/ZhiWei-Ou/xserial/internal/logging"
)

func main() {
	if err := cmd.Execute(); err != nil {
		logging.New(os.Stderr).Error("application.failed", "error", err)
		os.Exit(1)
	}
}
