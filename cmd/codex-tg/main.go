package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Nergous/codex-tg/internal/ipc"
	"github.com/Nergous/codex-tg/internal/launcher"
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
	"open":      runOpen,
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

func runOpen(args []string) error {
	newSession := false
	for len(args) > 0 && strings.HasPrefix(args[0], "--") {
		switch args[0] {
		case "--new":
			newSession = true
			args = args[1:]
		default:
			return fmt.Errorf("unknown open option %q", args[0])
		}
	}

	if len(args) != 1 {
		return fmt.Errorf("usage: open [--new] <project_path>")
	}
	projectPath := strings.TrimSpace(args[0])
	if projectPath == "" {
		return fmt.Errorf("project path is required")
	}
	absolutePath, err := filepath.Abs(projectPath)
	if err != nil {
		return fmt.Errorf("resolve project path: %w", err)
	}
	projectPath = absolutePath

	endpoint := os.Getenv("CODEX_TG_IPC_URL")
	if strings.TrimSpace(endpoint) == "" {
		endpoint = "http://127.0.0.1:4500"
	}
	token := os.Getenv("CODEX_TG_IPC_TOKEN")
	if strings.TrimSpace(token) == "" {
		return errors.New("missing CODEX_TG_IPC_TOKEN")
	}
	binary := os.Getenv("CODEX_TG_CODEX_BINARY")
	if strings.TrimSpace(binary) == "" {
		return errors.New("missing CODEX_TG_CODEX_BINARY")
	}

	client := ipc.NewClient(endpoint, token)
	response, err := client.Open(context.Background(), ipc.OpenRequest{
		ProjectPath: projectPath,
		NewSession:  newSession,
	})
	if err != nil {
		return err
	}

	return launcher.New(binary, response.Endpoint).Run(context.Background(), projectPath, response.ThreadID, response.Token)
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: codex-tg <setup|serve|open [--new] <path>|project|status|autostart>")
}
