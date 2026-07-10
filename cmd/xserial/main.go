package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/ZhiWei-Ou/xserial/internal/cmd"
	"github.com/ZhiWei-Ou/xserial/internal/logging"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := cmd.ExecuteContext(ctx); err != nil {
		logging.New(os.Stderr).Error("application.failed", "error", err)
		os.Exit(1)
	}
}
