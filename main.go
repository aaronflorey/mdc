package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/getsentry/sentry-go"

	"github.com/aaronflorey/mdc/internal/cli"
	"github.com/aaronflorey/mdc/internal/version"
)

const (
	exitFailure   = 1
	exitPanic     = 2
	exitInterrupt = 130
)

func main() {
	os.Exit(run())
}

func run() (code int) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// An empty DSN leaves Sentry as a no-op client.
	if err := sentry.Init(sentry.ClientOptions{Dsn: os.Getenv("SENTRY_DSN"), Release: version.Version}); err != nil {
		fmt.Fprintf(os.Stderr, "mdc: sentry disabled: %v\n", err)
	}
	defer sentry.Flush(2 * time.Second)

	defer func() {
		if r := recover(); r != nil {
			sentry.CurrentHub().Recover(r)
			fmt.Fprintf(os.Stderr, "mdc: internal error: %v\n", r)
			code = exitPanic
		}
	}()

	err := cli.Execute(ctx)
	if err == nil {
		return 0
	}
	if code, ok := cli.ExitCode(err); ok {
		return code
	}
	if errors.Is(err, context.Canceled) {
		return exitInterrupt
	}
	sentry.CaptureException(err)
	_, _ = lipgloss.Fprintln(os.Stderr, errorStyle().Render("Error: "+err.Error()))
	return exitFailure
}

func errorStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
}
