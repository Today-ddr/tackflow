package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/today-ddr/tackflow/internal/bridge"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := run(ctx, os.Args[1:], os.Getenv, os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, arguments []string, getenv func(string) string, output io.Writer) int {
	flags := flag.NewFlagSet("tack-bridge", flag.ContinueOnError)
	flags.SetOutput(output)
	once := flags.Bool("once", false, "Run one bounded cycle; persist unfinished work for the next invocation")
	check := flags.Bool("check", false, "Check configuration, authentication and reachability without claiming or dispatching")
	if err := flags.Parse(arguments); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if flags.NArg() != 0 || (*once && *check) {
		_, _ = fmt.Fprintln(output, "use --once, --check, or no flags for continuous polling")
		return 2
	}
	config, err := bridge.LoadConfig(getenv)
	if err != nil {
		_, _ = fmt.Fprintln(output, err)
		return 1
	}
	if *check {
		err = bridge.Check(ctx, config)
		if err == nil {
			_, _ = fmt.Fprintln(output, "Tack and Codeg checks passed; no work dispatched")
		}
	} else {
		var worker *bridge.Bridge
		worker, err = bridge.Open(config)
		if err == nil {
			err = worker.Run(ctx, *once)
			closeErr := worker.Close()
			if err == nil {
				err = closeErr
			}
		}
	}
	if errors.Is(err, context.Canceled) {
		return 0
	}
	if err != nil {
		_, _ = fmt.Fprintln(output, err)
		return 1
	}
	return 0
}
