package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Nergous/codex-tg/internal/app"
	"github.com/Nergous/codex-tg/internal/approval"
	"github.com/Nergous/codex-tg/internal/autostart"
	"github.com/Nergous/codex-tg/internal/codex"
	"github.com/Nergous/codex-tg/internal/config"
	"github.com/Nergous/codex-tg/internal/ipc"
	"github.com/Nergous/codex-tg/internal/launcher"
	"github.com/Nergous/codex-tg/internal/models"
	"github.com/Nergous/codex-tg/internal/secrets"
	"github.com/Nergous/codex-tg/internal/session"
	"github.com/Nergous/codex-tg/internal/state"
	"github.com/Nergous/codex-tg/internal/telegram"
)

const (
	exitOK = iota
	exitError
	exitUsage
)

var errCommandNotWired = errors.New("command not wired")

type commandHandler func(args []string) error

var commands = map[string]commandHandler{
	"setup":     runSetup,
	"serve":     runServe,
	"open":      runOpen,
	"project":   runProject,
	"status":    runStatus,
	"autostart": runAutostart,
}

func runSetup([]string) error {
	reader := bufio.NewReader(os.Stdin)
	read := func(label string) (string, error) {
		fmt.Fprint(os.Stdout, label)
		value, err := reader.ReadString('\n')
		return strings.TrimSpace(value), err
	}
	token, err := readSecret("Telegram bot token: ")
	if err != nil {
		return err
	}
	user, err := read("Allowed Telegram user ID: ")
	if err != nil {
		return err
	}
	userID, err := strconv.ParseInt(user, 10, 64)
	if err != nil {
		return err
	}
	chat, err := read("Allowed private chat ID: ")
	if err != nil {
		return err
	}
	chatID, err := strconv.ParseInt(chat, 10, 64)
	if err != nil {
		return err
	}
	name, err := read("First project name: ")
	if err != nil {
		return err
	}
	projectPath, err := read("First project path: ")
	if err != nil {
		return err
	}
	listen, err := read("App Server listen [127.0.0.1:4500]: ")
	if err != nil {
		return err
	}
	if listen == "" {
		listen = "127.0.0.1:4500"
	}
	binary, err := read("Codex executable: ")
	if err != nil {
		return err
	}
	cfg := &config.Config{Telegram: config.TelegramConfig{AllowedUserID: userID, AllowedChatID: chatID}, AppServer: config.AppServerConfig{Listen: listen, CodexBinary: binary}, Projects: []models.Project{{Name: name, Path: projectPath}}}
	bot := telegram.NewClient("https://api.telegram.org/bot"+token, token, nil)
	return app.Setup(context.Background(), bot, secrets.NewWindowsStore(), []byte(token), app.ConfigPath(), cfg)
}

func runStatus([]string) error {
	url, token := os.Getenv("CODEX_TG_IPC_URL"), os.Getenv("CODEX_TG_IPC_TOKEN")
	if url == "" || token == "" {
		return errors.New("missing CODEX_TG_IPC_URL or CODEX_TG_IPC_TOKEN")
	}
	status, err := ipc.NewClient(url, token).Status(context.Background())
	if err != nil {
		return err
	}
	fmt.Printf("running=%t thread=%s project=%s\n", status.Running, status.ThreadID, status.ProjectPath)
	return nil
}
func runProject(args []string) error {
	cfg, err := config.Load(app.ConfigPath())
	if err != nil {
		return err
	}
	if len(args) == 1 && args[0] == "list" {
		for _, p := range cfg.Projects {
			fmt.Printf("%s\t%s\n", p.Name, p.Path)
		}
		return nil
	}
	if len(args) == 3 && args[0] == "add" {
		cfg.Projects = append(cfg.Projects, models.Project{Name: args[1], Path: args[2]})
		return config.Save(app.ConfigPath(), cfg)
	}
	if len(args) == 2 && args[0] == "remove" {
		kept := cfg.Projects[:0]
		for _, p := range cfg.Projects {
			if p.Name != args[1] {
				kept = append(kept, p)
			}
		}
		cfg.Projects = kept
		return config.Save(app.ConfigPath(), cfg)
	}
	return errors.New("usage: project <list|add <name> <path>|remove <name>>")
}
func runAutostart(args []string) error {
	if len(args) != 1 {
		return errors.New("usage: autostart <install|remove|status>")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	scheduler := autostart.Scheduler{Executable: exe, WorkDir: filepath.Dir(app.ConfigPath())}
	switch args[0] {
	case "install":
		return scheduler.Install(context.Background())
	case "remove":
		return scheduler.Remove(context.Background())
	case "status":
		ok, err := scheduler.Status(context.Background())
		if err == nil {
			fmt.Println(ok)
		}
		return err
	default:
		return errors.New("usage: autostart <install|remove|status>")
	}
}

type pollingStore struct {
	client *telegram.Client
	store  *state.Store
}

func (p pollingStore) GetUpdates(ctx context.Context, offset int64) ([]telegram.Update, error) {
	return p.client.GetUpdates(ctx, offset)
}
func (p pollingStore) UpdateOffset(ctx context.Context) (int64, error) {
	return p.store.UpdateOffset(ctx)
}
func (p pollingStore) SaveUpdateOffset(ctx context.Context, offset int64) error {
	return p.store.SaveUpdateOffset(ctx, offset)
}

func runServe([]string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	cfg, err := config.Load(app.ConfigPath())
	if err != nil {
		return err
	}
	store, err := state.Open(ctx, app.DataPath())
	if err != nil {
		return err
	}
	defer store.Close()
	for i := range cfg.Projects {
		if err := store.PutProject(ctx, &cfg.Projects[i]); err != nil {
			return err
		}
	}
	secretsStore := secrets.NewWindowsStore()
	token, err := secretsStore.Get(ctx, secrets.TelegramBotToken)
	if err != nil {
		return err
	}
	supervisor := &codex.Supervisor{Binary: cfg.AppServer.CodexBinary, Listen: cfg.AppServer.Listen}
	service := app.New(supervisor)
	var coordinator *session.Coordinator
	var handler *telegram.Handler
	var codexEvents <-chan codex.Event
	service.Configure(func(ctx context.Context, path string, fresh bool) (string, error) {
		if coordinator == nil {
			return "", errors.New("service initializing")
		}
		s, err := coordinator.OpenProject(ctx, path, fresh)
		return s.ThreadID, err
	}, func(ctx context.Context) error { return store.FaultRunningTurns(ctx) }, func(ctx context.Context, endpoint codex.AppServerEndpoint) error {
		client, err := codex.Dial(ctx, endpoint.URL, endpoint.Token)
		if err != nil {
			return err
		}
		if err = client.Initialize(ctx, codex.ClientInfo{Name: "codex-tg", Title: "Codex Telegram Bridge", Version: "0.1.0"}); err != nil {
			return err
		}
		coordinator = session.New(client, store, cfg.Projects)
		bot := telegram.NewClient("https://api.telegram.org/bot"+string(token), string(token), nil)
		handler = telegram.NewHandler(telegram.HandlerOptions{Coordinator: coordinator, Messenger: bot, AllowedUserID: cfg.Telegram.AllowedUserID, AllowedChatID: cfg.Telegram.AllowedChatID, ApprovalService: approval.New(store), ApprovalResponder: client, LockStore: store})
		codexEvents = client.Events()
		return nil
	})
	if err := service.Start(ctx); err != nil {
		return err
	}
	defer service.Stop(context.Background())
	controlToken, err := randomControlToken()
	if err != nil {
		return err
	}
	ipcAddress, err := service.StartIPC(ctx, controlToken)
	if err != nil {
		return err
	}
	runtimePath := app.RuntimePath()
	if err := app.SaveRuntime(runtimePath, app.RuntimeInfo{IPCURL: ipcAddress, IPCToken: controlToken, CodexBinary: cfg.AppServer.CodexBinary}); err != nil {
		return err
	}
	defer app.RemoveRuntime(runtimePath)
	bot := telegram.NewClient("https://api.telegram.org/bot"+string(token), string(token), nil)
	return service.RunBridge(ctx, pollingStore{client: bot, store: store}, handler.Handle, codexEvents, handler.OnEvent, coordinator.Complete)
}
func randomControlToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
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

	runtime, err := loadOpenRuntime()
	if err != nil {
		return err
	}

	client := ipc.NewClient(runtime.IPCURL, runtime.IPCToken)
	response, err := client.Open(context.Background(), ipc.OpenRequest{
		ProjectPath: projectPath,
		NewSession:  newSession,
	})
	if err != nil {
		return err
	}

	return launcher.New(runtime.CodexBinary, response.Endpoint).Run(context.Background(), projectPath, response.ThreadID, response.Token)
}

func loadOpenRuntime() (app.RuntimeInfo, error) {
	fromEnv := app.RuntimeInfo{
		IPCURL:      strings.TrimSpace(os.Getenv("CODEX_TG_IPC_URL")),
		IPCToken:    strings.TrimSpace(os.Getenv("CODEX_TG_IPC_TOKEN")),
		CodexBinary: strings.TrimSpace(os.Getenv("CODEX_TG_CODEX_BINARY")),
	}
	if fromEnv.IPCURL != "" || fromEnv.IPCToken != "" || fromEnv.CodexBinary != "" {
		if fromEnv.IPCURL == "" || fromEnv.IPCToken == "" || fromEnv.CodexBinary == "" {
			return app.RuntimeInfo{}, errors.New("incomplete IPC environment configuration")
		}
		return fromEnv, nil
	}
	return app.LoadRuntime(app.RuntimePath())
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: codex-tg <setup|serve|open [--new] <path>|project|status|autostart>")
}
