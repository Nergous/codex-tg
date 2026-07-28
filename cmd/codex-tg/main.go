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
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/Nergous/codex-tg/internal/app"
	"github.com/Nergous/codex-tg/internal/approval"
	"github.com/Nergous/codex-tg/internal/autostart"
	"github.com/Nergous/codex-tg/internal/codex"
	"github.com/Nergous/codex-tg/internal/config"
	"github.com/Nergous/codex-tg/internal/install"
	"github.com/Nergous/codex-tg/internal/ipc"
	"github.com/Nergous/codex-tg/internal/launcher"
	"github.com/Nergous/codex-tg/internal/models"
	"github.com/Nergous/codex-tg/internal/onboarding"
	"github.com/Nergous/codex-tg/internal/pairing"
	"github.com/Nergous/codex-tg/internal/secrets"
	background "github.com/Nergous/codex-tg/internal/service"
	"github.com/Nergous/codex-tg/internal/session"
	"github.com/Nergous/codex-tg/internal/state"
	"github.com/Nergous/codex-tg/internal/telegram"
)

const (
	exitOK = iota
	exitError
	exitUsage
)

type commandHandler func(args []string) error

var commands = map[string]commandHandler{
	"setup":     runSetup,
	"serve":     runServe,
	"open":      runOpen,
	"project":   runProject,
	"status":    runStatus,
	"autostart": runAutostart,
}

var launchTUI = func(ctx context.Context, binary, endpoint, cwd, threadID, token string) error {
	return launcher.New(binary, endpoint).Run(ctx, cwd, threadID, token)
}

var (
	getwd         = os.Getwd
	primaryFlow   = runPrimaryFlow
	ensureRuntime = func(ctx context.Context) (app.RuntimeInfo, error) {
		return background.DefaultManager(app.RuntimePath()).Ensure(ctx)
	}
)

func runSetup([]string) error {
	reader := bufio.NewReader(os.Stdin)
	read := func(label string) (string, error) {
		fmt.Fprint(os.Stdout, label)
		value, err := reader.ReadString('\n')
		if errors.Is(err, io.EOF) && value != "" {
			err = nil
		}
		return strings.TrimSpace(value), err
	}
	yes := func(label string, defaultYes bool) (bool, error) {
		value, err := read(label)
		if err != nil {
			return false, err
		}
		if value == "" {
			return defaultYes, nil
		}
		value = strings.ToLower(value)
		return value == "y" || value == "yes", nil
	}
	progress, err := onboarding.LoadState(app.OnboardingPath())
	if err != nil {
		return err
	}
	checkpoint := func() error { return onboarding.SaveState(app.OnboardingPath(), progress) }

	if !progress.CommandLineComplete {
		approved, err := yes("Install codex-tg for current user? [y/N]: ", false)
		if err != nil {
			return err
		}
		if approved {
			source, err := os.Executable()
			if err != nil {
				return err
			}
			target, err := install.Target()
			if err != nil {
				return err
			}
			if err := install.CopyExecutable(source, target, true); err != nil {
				return err
			}
			if err := install.AddToUserPath(filepath.Dir(target)); err != nil {
				fmt.Fprintf(os.Stderr, "PATH update failed: %v\nAdd manually: %s\n", err, filepath.Dir(target))
			} else {
				fmt.Fprintln(os.Stdout, "codex-tg was installed for the current user. Restart your terminal to use it from PATH.")
			}
		}
		progress.CommandLineComplete = true
		if err := checkpoint(); err != nil {
			return err
		}
	}

	var cfg *config.Config
	if progress.TelegramComplete {
		cfg, err = config.Load(app.ConfigPath())
		if err != nil {
			return err
		}
	}
	for !progress.TelegramComplete {
		token, err := readSecret("Telegram bot token: ")
		if err != nil {
			return err
		}
		bot := telegram.NewClient("https://api.telegram.org/bot"+token, token, nil)
		for {
			fmt.Fprintln(os.Stdout, "Validating token and preparing Telegram pairing...")
			pairCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			identity, pairErr := pairing.PairWithReady(pairCtx, bot, func(bot pairing.BotIdentity) {
				fmt.Fprintf(os.Stdout, "Token validated. Open @%s in Telegram and send /start.\n", bot.Username)
			}, func(bot pairing.BotIdentity, sender pairing.Identity) (bool, error) {
				return yes(fmt.Sprintf("Bind @%s to Telegram @%s (user %d, chat %d)? [y/N]: ", bot.Username, sender.Username, sender.UserID, sender.ChatID), false)
			})
			cancel()
			if pairErr != nil {
				retry, readErr := yes(fmt.Sprintf("Pairing failed: %v. Retry? [Y/n]: ", pairErr), true)
				if readErr != nil {
					return readErr
				}
				if retry {
					continue
				}
				return pairErr
			}
			binary, err := exec.LookPath("codex")
			if err != nil {
				return errors.New("codex executable not found in PATH; install Codex and run `codex-tg setup` again")
			}
			binary, err = filepath.Abs(binary)
			if err != nil {
				return err
			}
			cfg = &config.Config{Telegram: config.TelegramConfig{AllowedUserID: identity.UserID, AllowedChatID: identity.ChatID}, AppServer: config.AppServerConfig{Listen: "127.0.0.1:4500", CodexBinary: binary}, Projects: []models.Project{}}
			if err := secrets.NewSystemStore().Set(context.Background(), secrets.TelegramBotToken, []byte(token)); err != nil {
				return err
			}
			if err := config.Save(app.ConfigPath(), cfg); err != nil {
				return err
			}
			pairingStore, err := state.Open(context.Background(), app.DataPath())
			if err != nil {
				return fmt.Errorf("open state for Telegram pairing: %w", err)
			}
			if err := pairingStore.SaveUpdateOffset(context.Background(), identity.UpdateOffset); err != nil {
				_ = pairingStore.Close()
				return err
			}
			if err := pairingStore.Close(); err != nil {
				return err
			}
			progress.TelegramComplete = true
			if err := checkpoint(); err != nil {
				return err
			}
			break
		}
	}

	if !progress.ServiceComplete {
		approved, err := yes("Enable background service autostart? [Y/n]: ", true)
		if err != nil {
			return err
		}
		if approved {
			executable, err := os.Executable()
			if err != nil {
				return err
			}
			executable = install.PreferredExecutable(executable)
			scheduler := autostart.Scheduler{Executable: executable, WorkDir: filepath.Dir(app.ConfigPath())}
			if err := scheduler.Install(context.Background()); err != nil {
				fmt.Fprintf(os.Stderr, "Autostart failed: %v\nUse `codex-tg serve` for foreground mode.\n", err)
			}
		}
		progress.ServiceComplete = true
		if err := checkpoint(); err != nil {
			return err
		}
	}
	if !progress.ProjectComplete {
		cwd, err := getwd()
		if err != nil {
			return err
		}
		_, added, err := onboarding.EnsureProject(cfg, cwd, func(prompt string) (bool, error) { return yes(strings.Replace(prompt, "[y/N]", "[Y/n]", 1), true) })
		if err != nil {
			return err
		}
		if added {
			if err := config.Save(app.ConfigPath(), cfg); err != nil {
				return err
			}
		}
		progress.ProjectComplete = true
		if err := checkpoint(); err != nil {
			return err
		}
	}
	return nil
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
	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
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
	secretsStore := secrets.NewSystemStore()
	token, err := secretsStore.Get(ctx, secrets.TelegramBotToken)
	if err != nil {
		return err
	}
	supervisor := &codex.Supervisor{Binary: cfg.AppServer.CodexBinary, Listen: cfg.AppServer.Listen}
	service := app.New(supervisor)
	var coordinator *session.Coordinator
	var handler *telegram.Handler
	var codexEvents <-chan codex.Event
	var codexErrors <-chan error
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
		codexErrors = client.Errors()
		return nil
	})
	service.ConfigureInteractive(func(_ context.Context, path string) error {
		if coordinator == nil {
			return errors.New("service initializing")
		}
		return coordinator.PrepareProject(path)
	}, func(ctx context.Context, path, threadID string) error {
		if coordinator == nil {
			return errors.New("service initializing")
		}
		if err := coordinator.AdoptThread(ctx, path, threadID); err != nil {
			return err
		}
		if handler != nil {
			handler.AdoptThread(cfg.Telegram.AllowedChatID, path, threadID)
		}
		return nil
	})
	service.ConfigureProjectRegistration(func(ctx context.Context, project ipc.ProjectRequest) error {
		if coordinator == nil {
			return errors.New("service initializing")
		}
		return coordinator.AddProject(ctx, models.Project{Name: project.Name, Path: project.Path, Enabled: project.Enabled})
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
	writeServeStatus(os.Stdout, cfg.AppServer.Listen, ipcAddress, runtimePath, len(cfg.Projects))
	bot := telegram.NewClient("https://api.telegram.org/bot"+string(token), string(token), nil)
	handleCodexEvent := func(ctx context.Context, event codex.Event) error {
		if event.Method == "thread/started" {
			if err := service.AdoptInteractiveThread(ctx, event.ThreadID); err != nil {
				return err
			}
		}
		return handler.OnEvent(ctx, event)
	}
	err = service.RunBridge(ctx, pollingStore{client: bot, store: store}, handler.Handle, codexEvents, codexErrors, handleCodexEvent, coordinator.Complete)
	if logs := strings.TrimSpace(supervisor.Logs()); err != nil && logs != "" {
		return fmt.Errorf("%w\nApp Server log:\n%s", err, logs)
	}
	return err
}

func writeServeStatus(w io.Writer, appServer, ipcAddress, runtimePath string, projects int) {
	fmt.Fprintln(w, "codex-tg is running")
	fmt.Fprintf(w, "  App Server: %s\n", appServer)
	fmt.Fprintf(w, "  IPC: %s\n", ipcAddress)
	fmt.Fprintf(w, "  Projects: %d\n", projects)
	fmt.Fprintf(w, "  Runtime: %s\n", runtimePath)
	fmt.Fprintln(w, "Press Ctrl+C to stop.")
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
		if err := primaryFlow(); err != nil {
			fmt.Fprintf(stderr, "codex-tg: %v\n", err)
			return exitError
		}
		return exitOK
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

	if len(args) > 1 {
		return fmt.Errorf("usage: open [--new] [project_path]")
	}
	projectPath := ""
	if len(args) == 1 {
		projectPath = strings.TrimSpace(args[0])
	}
	if projectPath == "" {
		var err error
		projectPath, err = getwd()
		if err != nil {
			return fmt.Errorf("resolve current directory: %w", err)
		}
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
		Interactive: true,
	})
	if err != nil {
		return err
	}

	return launchTUI(context.Background(), runtime.CodexBinary, response.Endpoint, projectPath, response.ThreadID, response.Token)
}

func runPrimaryFlow() error {
	cwd, err := getwd()
	if err != nil {
		return fmt.Errorf("resolve current directory: %w", err)
	}
	cfg, err := config.Load(app.ConfigPath())
	if errors.Is(err, config.ErrConfigNotFound) {
		if err := runSetup(nil); err != nil {
			return err
		}
		cfg, err = config.Load(app.ConfigPath())
	}
	if err != nil {
		return err
	}
	reader := bufio.NewReader(os.Stdin)
	project, added, err := onboarding.EnsureProject(cfg, cwd, func(prompt string) (bool, error) {
		fmt.Fprint(os.Stdout, prompt)
		answer, readErr := reader.ReadString('\n')
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return false, readErr
		}
		answer = strings.ToLower(strings.TrimSpace(answer))
		return answer == "y" || answer == "yes", nil
	})
	if err != nil {
		return err
	}
	if added {
		if err := config.Save(app.ConfigPath(), cfg); err != nil {
			return err
		}
		runtime, err := loadOpenRuntime()
		if err != nil {
			return err
		}
		if err := ipc.NewClient(runtime.IPCURL, runtime.IPCToken).RegisterProject(context.Background(), ipc.ProjectRequest{Name: project.Name, Path: project.Path, Enabled: project.Enabled}); err != nil {
			return fmt.Errorf("register project with running service: %w", err)
		}
	} else {
		found := false
		for _, configured := range cfg.Projects {
			if configured.Path == project.Path {
				found = true
				break
			}
		}
		if !found {
			return errors.New("current project is not registered")
		}
	}
	return runOpen([]string{cwd})
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
	return ensureRuntime(context.Background())
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: codex-tg [setup|serve|open [--new] [path]|project|status|autostart]")
}
