package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strconv"

	"git-retime/internal/retime"
)

func run(args []string) error {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		_, err := fmt.Fprintln(os.Stdout, "Usage: git retime [count]\n\nEdit author and committer dates for the last count first-parent commits (default: 10).\nRequires macOS/Linux and Git 2.48+. Uses git var GIT_EDITOR.")
		return err
	}
	n := 10
	if len(args) > 1 {
		return fmt.Errorf("usage: git retime [count]")
	}
	if len(args) == 1 {
		var err error
		n, err = strconv.Atoi(args[0])
		if err != nil || n <= 0 {
			return fmt.Errorf("count must be a positive integer")
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return retime.Run(ctx, ".", n, os.Stdin, os.Stdout, os.Stderr)
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "git-retime:", err)
		os.Exit(1)
	}
}
