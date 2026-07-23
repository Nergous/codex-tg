package main

import (
	"errors"
	"fmt"
	"io"
	"os"
)

const (
	exitOK = iota
	exitError
	exitUsage
)

var errCommandNotWired = errors.New("command not wired")

type commandHandler func(args []string) error

var commands = map[string]commandHandler{
	"setup":     notWired,
	"serve":     notWired,
	"open":      notWired,
	"project":   notWired,
	"status":    notWired,
	"autostart": notWired,
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printUsage(stderr)
		return exitUsage
	}

	if args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		printUsage(stdout)
		return exitOK
	}

	handler, ok := commands[args[0]]
	if !ok {
		fmt.Fprintf(stderr, "unknown command %q\n", args[0])
		printUsage(stderr)
		return exitUsage
	}

	if err := handler(args[1:]); err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", args[0], err)
		return exitError
	}
	return exitOK
}

func notWired([]string) error {
	return errCommandNotWired
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: codex-tg <setup|serve|open|project|status|autostart>")
}
