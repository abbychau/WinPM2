package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	pipeName         = `\\.\pipe\winpm2`
	runKeyPath       = `Software\Microsoft\Windows\CurrentVersion\Run`
	runValueName     = "winpm2"
	maxRestartsInMin = 10
)

type Ecosystem struct {
	Apps []AppConfig `json:"apps"`
}

type AppConfig struct {
	Name        string         `json:"name"`
	Script      string         `json:"script"`
	Args        []string       `json:"args"`
	Cwd         string         `json:"cwd"`
	Env         map[string]any `json:"env"`
	Watch       bool           `json:"watch"`
	Autorestart *bool          `json:"autorestart,omitempty"`
}

type ManagedProc struct {
	Config       AppConfig
	Cmd          *exec.Cmd
	PID          int
	Status       string
	Desired      bool
	RestartCount int
	StartedAt    time.Time
	StartTimes   []time.Time
	outFile      *os.File
	errFile      *os.File
}

type AppStatus struct {
	Name     string `json:"name"`
	PID      int    `json:"pid"`
	Status   string `json:"status"`
	Restarts int    `json:"restarts"`
	Uptime   int64  `json:"uptime_seconds"`
}

type AppDescribe struct {
	Name        string         `json:"name"`
	PID         int            `json:"pid"`
	Status      string         `json:"status"`
	Restarts    int            `json:"restarts"`
	Uptime      int64          `json:"uptime_seconds"`
	Desired     bool           `json:"desired"`
	Script      string         `json:"script"`
	Args        []string       `json:"args"`
	Cwd         string         `json:"cwd"`
	Env         map[string]any `json:"env"`
	LogOut      string         `json:"log_out"`
	LogErr      string         `json:"log_err"`
	Autorestart bool           `json:"autorestart"`
}

type ipcRequest struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

type ipcResponse struct {
	OK       bool         `json:"ok"`
	Message  string       `json:"message"`
	Apps     []AppStatus  `json:"apps,omitempty"`
	Describe *AppDescribe `json:"describe,omitempty"`
}

type Manager struct {
	mu       sync.Mutex
	apps     map[string]*ManagedProc
	stateDir string
	logsDir  string
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		return
	}

	cmd := strings.ToLower(os.Args[1])
	args := os.Args[2:]
	if cmd == "ls" {
		cmd = "list"
	}

	switch cmd {
	case "daemon":
		runDaemon(args)
	case "startup":
		runStartup(args)
	case "start", "stop", "restart", "delete", "list", "describe", "save", "resurrect":
		runClientCommand(cmd, args)
	default:
		printUsage()
	}
}

func printUsage() {
	fmt.Println("winpm2 - lightweight Windows process manager")
	fmt.Println("usage:")
	fmt.Println("  winpm2 daemon [--autoload]")
	fmt.Println("  winpm2 startup install|uninstall|status")
	fmt.Println("  winpm2 start <ecosystem.json|name>")
	fmt.Println("  winpm2 stop <name|all|ecosystem.json>")
	fmt.Println("  winpm2 restart <name|all|ecosystem.json>")
	fmt.Println("  winpm2 delete <name|all|ecosystem.json>")
	fmt.Println("  winpm2 list")
	fmt.Println("  winpm2 ls")
	fmt.Println("  winpm2 describe <name|ecosystem.json>")
	fmt.Println("  winpm2 save")
	fmt.Println("  winpm2 resurrect")
}

func runDaemon(args []string) {
	stateDir, logsDir, err := ensureDirs()
	if err != nil {
		fmt.Printf("failed to initialize state dir: %v\n", err)
		os.Exit(1)
	}

	mgr := &Manager{
		apps:     map[string]*ManagedProc{},
		stateDir: stateDir,
		logsDir:  logsDir,
	}

	autoload := false
	for _, a := range args {
		if a == "--autoload" {
			autoload = true
		}
	}
	if autoload {
		if _, err := mgr.resurrect(); err != nil {
			fmt.Printf("autoload warning: %v\n", err)
		}
	}

	listener, err := winio.ListenPipe(pipeName, nil)
	if err != nil {
		fmt.Printf("failed to listen on named pipe (daemon already running?): %v\n", err)
		os.Exit(1)
	}
	defer listener.Close()

	fmt.Println("winpm2 daemon started")
	for {
		conn, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		go handleConn(mgr, conn)
	}
}

func runClientCommand(command string, args []string) {
	if command == "start" && len(args) == 1 {
		if _, err := os.Stat(args[0]); err == nil {
			if abs, err := filepath.Abs(args[0]); err == nil {
				args[0] = abs
			}
		}
	}

	req := ipcRequest{Command: command, Args: args}
	resp, err := callDaemon(req)
	if err != nil {
		if err := startDaemonDetached(); err != nil {
			fmt.Printf("failed to contact daemon and failed to start one: %v\n", err)
			os.Exit(1)
		}
		for i := 0; i < 10; i++ {
			time.Sleep(200 * time.Millisecond)
			resp, err = callDaemon(req)
			if err == nil {
				break
			}
		}
		if err != nil {
			fmt.Printf("daemon is not reachable: %v\n", err)
			os.Exit(1)
		}
	}

	if !resp.OK {
		fmt.Printf("error: %s\n", resp.Message)
		os.Exit(1)
	}

	if command == "list" {
		if len(resp.Apps) == 0 {
			fmt.Println("no managed processes")
			return
		}
		fmt.Printf("%-24s %-8s %-10s %-10s %-10s\n", "name", "pid", "status", "restarts", "uptime")
		for _, app := range resp.Apps {
			fmt.Printf("%-24s %-8d %-10s %-10d %-10ds\n", app.Name, app.PID, app.Status, app.Restarts, app.Uptime)
		}
		return
	}

	if command == "describe" {
		if resp.Describe == nil {
			fmt.Println("no data")
			return
		}
		d := resp.Describe
		fmt.Printf("name:         %s\n", d.Name)
		fmt.Printf("pid:          %d\n", d.PID)
		fmt.Printf("status:       %s\n", d.Status)
		fmt.Printf("uptime:       %ds\n", d.Uptime)
		fmt.Printf("restarts:     %d\n", d.Restarts)
		fmt.Printf("desired:      %t\n", d.Desired)
		fmt.Printf("script:       %s\n", d.Script)
		fmt.Printf("args:         %s\n", strings.Join(d.Args, " "))
		fmt.Printf("cwd:          %s\n", d.Cwd)
		fmt.Printf("autorestart:  %t\n", d.Autorestart)
		fmt.Printf("out log:      %s\n", d.LogOut)
		fmt.Printf("err log:      %s\n", d.LogErr)
		if len(d.Env) == 0 {
			fmt.Println("env:          (none)")
			return
		}
		fmt.Println("env:")
		for k, v := range d.Env {
			fmt.Printf("  %s=%v\n", k, v)
		}
		return
	}

	if resp.Message != "" {
		fmt.Println(resp.Message)
	}
}

func runStartup(args []string) {
	if len(args) != 1 {
		fmt.Println("usage: winpm2 startup install|uninstall|status")
		os.Exit(1)
	}

	action := strings.ToLower(args[0])
	switch action {
	case "install":
		if !isElevated() {
			fmt.Println("startup install expects an elevated shell. please run as Administrator.")
			os.Exit(1)
		}
		if err := installStartupRunKey(); err != nil {
			fmt.Printf("failed to install startup: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("startup installed in HKCU Run")
	case "uninstall":
		if !isElevated() {
			fmt.Println("startup uninstall expects an elevated shell. please run as Administrator.")
			os.Exit(1)
		}
		if err := uninstallStartupRunKey(); err != nil {
			fmt.Printf("failed to uninstall startup: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("startup entry removed")
	case "status":
		value, err := startupStatus()
		if err != nil {
			fmt.Println("startup is not installed")
			return
		}
		fmt.Printf("startup installed: %s\n", value)
	default:
		fmt.Println("usage: winpm2 startup install|uninstall|status")
		os.Exit(1)
	}
}

func callDaemon(req ipcRequest) (ipcResponse, error) {
	timeout := 2 * time.Second
	conn, err := winio.DialPipe(pipeName, &timeout)
	if err != nil {
		return ipcResponse{}, err
	}
	defer conn.Close()

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return ipcResponse{}, err
	}

	var resp ipcResponse
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return ipcResponse{}, err
	}
	return resp, nil
}

func startDaemonDetached() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}

	cmd := exec.Command(exe, "daemon", "--autoload")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS | windows.CREATE_NO_WINDOW,
		HideWindow:    true,
	}
	return cmd.Start()
}

func handleConn(mgr *Manager, conn net.Conn) {
	defer conn.Close()

	var req ipcRequest
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		_ = json.NewEncoder(conn).Encode(ipcResponse{OK: false, Message: err.Error()})
		return
	}

	resp := mgr.handleRequest(req)
	_ = json.NewEncoder(conn).Encode(resp)
}

func (m *Manager) handleRequest(req ipcRequest) ipcResponse {
	switch req.Command {
	case "start":
		if len(req.Args) != 1 {
			return ipcResponse{OK: false, Message: "usage: winpm2 start <ecosystem.json|name>"}
		}
		count, err := m.startByTarget(req.Args[0])
		if err != nil {
			return ipcResponse{OK: false, Message: err.Error()}
		}
		return ipcResponse{OK: true, Message: fmt.Sprintf("started %d app(s)", count)}
	case "stop":
		if len(req.Args) != 1 {
			return ipcResponse{OK: false, Message: "usage: winpm2 stop <name|all|ecosystem.json>"}
		}
		count, err := m.stop(req.Args[0])
		if err != nil {
			return ipcResponse{OK: false, Message: err.Error()}
		}
		return ipcResponse{OK: true, Message: fmt.Sprintf("stopped %d app(s)", count)}
	case "restart":
		if len(req.Args) != 1 {
			return ipcResponse{OK: false, Message: "usage: winpm2 restart <name|all|ecosystem.json>"}
		}
		count, err := m.restart(req.Args[0])
		if err != nil {
			return ipcResponse{OK: false, Message: err.Error()}
		}
		return ipcResponse{OK: true, Message: fmt.Sprintf("restarted %d app(s)", count)}
	case "delete":
		if len(req.Args) != 1 {
			return ipcResponse{OK: false, Message: "usage: winpm2 delete <name|all|ecosystem.json>"}
		}
		count, err := m.delete(req.Args[0])
		if err != nil {
			return ipcResponse{OK: false, Message: err.Error()}
		}
		return ipcResponse{OK: true, Message: fmt.Sprintf("deleted %d app(s)", count)}
	case "list":
		return ipcResponse{OK: true, Apps: m.list()}
	case "describe":
		if len(req.Args) != 1 {
			return ipcResponse{OK: false, Message: "usage: winpm2 describe <name|ecosystem.json>"}
		}
		app, err := m.describe(req.Args[0])
		if err != nil {
			return ipcResponse{OK: false, Message: err.Error()}
		}
		return ipcResponse{OK: true, Describe: &app}
	case "save":
		count, err := m.save()
		if err != nil {
			return ipcResponse{OK: false, Message: err.Error()}
		}
		return ipcResponse{OK: true, Message: fmt.Sprintf("saved %d app(s)", count)}
	case "resurrect":
		count, err := m.resurrect()
		if err != nil {
			return ipcResponse{OK: false, Message: err.Error()}
		}
		return ipcResponse{OK: true, Message: fmt.Sprintf("resurrected %d app(s)", count)}
	default:
		return ipcResponse{OK: false, Message: "unknown command"}
	}
}

func (m *Manager) startByTarget(target string) (int, error) {
	if _, err := os.Stat(target); err == nil {
		eco, err := readEcosystem(target)
		if err != nil {
			return 0, err
		}
		count := 0
		for _, app := range eco.Apps {
			if err := m.startApp(app); err != nil {
				return count, err
			}
			count++
		}
		return count, nil
	}

	dumpFile := filepath.Join(m.stateDir, "dump.json")
	eco, err := readEcosystem(dumpFile)
	if err != nil {
		return 0, fmt.Errorf("target is not a file and saved dump is unavailable: %w", err)
	}
	for _, app := range eco.Apps {
		if app.Name == target {
			return 1, m.startApp(app)
		}
	}
	return 0, fmt.Errorf("app %q not found", target)
}

func (m *Manager) startApp(cfg AppConfig) error {
	if strings.TrimSpace(cfg.Name) == "" {
		return errors.New("app name is required")
	}
	if strings.TrimSpace(cfg.Script) == "" {
		return fmt.Errorf("script is required for app %q", cfg.Name)
	}

	if cfg.Watch {
		fmt.Printf("watch ignored for app %s\n", cfg.Name)
	}

	m.mu.Lock()
	mp, exists := m.apps[cfg.Name]
	if exists && mp.Status == "online" {
		m.mu.Unlock()
		return fmt.Errorf("app %q is already online", cfg.Name)
	}
	if !exists {
		mp = &ManagedProc{Config: cfg}
		m.apps[cfg.Name] = mp
	} else {
		mp.Config = cfg
	}
	mp.Desired = true
	m.mu.Unlock()

	return m.spawn(cfg.Name)
}

func (m *Manager) spawn(name string) error {
	m.mu.Lock()
	mp, ok := m.apps[name]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("app %q not found", name)
	}
	cfg := mp.Config
	m.mu.Unlock()

	outPath := filepath.Join(m.logsDir, fmt.Sprintf("%s-out.log", cfg.Name))
	errPath := filepath.Join(m.logsDir, fmt.Sprintf("%s-err.log", cfg.Name))
	outFile, err := os.OpenFile(outPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	errFile, err := os.OpenFile(errPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		outFile.Close()
		return err
	}

	cmd := exec.Command(cfg.Script, cfg.Args...)
	if cfg.Cwd != "" {
		cmd.Dir = cfg.Cwd
	}
	cmd.Stdout = outFile
	cmd.Stderr = errFile
	cmd.Env = mergedEnv(cfg.Env)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW,
		HideWindow:    true,
	}

	if err := cmd.Start(); err != nil {
		outFile.Close()
		errFile.Close()
		return err
	}

	m.mu.Lock()
	mp = m.apps[name]
	mp.Cmd = cmd
	mp.PID = cmd.Process.Pid
	mp.Status = "online"
	mp.StartedAt = time.Now()
	mp.outFile = outFile
	mp.errFile = errFile
	mp.StartTimes = append(mp.StartTimes, time.Now())
	mp.StartTimes = trimOldStarts(mp.StartTimes)
	m.mu.Unlock()

	go m.waitAndMaybeRestart(name, cmd, outFile, errFile)
	return nil
}

func (m *Manager) waitAndMaybeRestart(name string, cmd *exec.Cmd, outFile, errFile *os.File) {
	err := cmd.Wait()
	_ = outFile.Close()
	_ = errFile.Close()

	m.mu.Lock()
	mp, ok := m.apps[name]
	if !ok {
		m.mu.Unlock()
		return
	}
	if mp.Cmd != cmd {
		m.mu.Unlock()
		return
	}
	mp.Cmd = nil
	mp.PID = 0
	mp.outFile = nil
	mp.errFile = nil

	autorestart := true
	if mp.Config.Autorestart != nil {
		autorestart = *mp.Config.Autorestart
	}
	requestedStop := !mp.Desired || mp.Status == "stopped"
	shouldRestart := mp.Desired && autorestart && !requestedStop
	mp.Status = "stopped"
	if err != nil && !requestedStop {
		mp.Status = "errored"
	}

	mp.StartTimes = trimOldStarts(mp.StartTimes)
	if len(mp.StartTimes) > maxRestartsInMin {
		mp.Desired = false
		shouldRestart = false
		mp.Status = "errored"
	}
	if shouldRestart {
		mp.RestartCount++
	}
	restartCount := mp.RestartCount
	m.mu.Unlock()

	if !shouldRestart {
		return
	}

	delay := restartDelay(restartCount)
	time.Sleep(delay)
	_ = m.spawn(name)
}

func (m *Manager) stop(target string) (int, error) {
	names, err := m.resolveTargets(target)
	if err != nil {
		return 0, err
	}

	m.mu.Lock()
	for _, name := range names {
		if mp, ok := m.apps[name]; ok {
			mp.Desired = false
		}
	}
	m.mu.Unlock()

	stopped := 0
	for _, name := range names {
		if m.kill(name) {
			stopped++
		}
	}

	m.mu.Lock()
	for _, name := range names {
		if mp, ok := m.apps[name]; ok {
			mp.Desired = false
			mp.Status = "stopped"
			mp.PID = 0
			mp.Cmd = nil
		}
	}
	m.mu.Unlock()

	return stopped, nil
}

func (m *Manager) restart(target string) (int, error) {
	names, err := m.resolveTargets(target)
	if err != nil {
		return 0, err
	}

	m.mu.Lock()
	configs := make([]AppConfig, 0, len(names))
	for _, n := range names {
		configs = append(configs, m.apps[n].Config)
	}
	m.mu.Unlock()

	if _, err := m.stop(target); err != nil {
		return 0, err
	}
	count := 0
	for _, cfg := range configs {
		if err := m.startApp(cfg); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

func (m *Manager) delete(target string) (int, error) {
	names, err := m.resolveTargets(target)
	if err != nil {
		return 0, err
	}

	if _, err := m.stop(target); err != nil {
		return 0, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for _, n := range names {
		delete(m.apps, n)
	}
	return len(names), nil
}

func (m *Manager) list() []AppStatus {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := make([]AppStatus, 0, len(m.apps))
	now := time.Now()
	for _, mp := range m.apps {
		uptime := int64(0)
		status := effectiveStatus(mp)
		if status == "online" && !mp.StartedAt.IsZero() {
			uptime = int64(now.Sub(mp.StartedAt).Seconds())
		}
		result = append(result, AppStatus{
			Name:     mp.Config.Name,
			PID:      mp.PID,
			Status:   status,
			Restarts: mp.RestartCount,
			Uptime:   uptime,
		})
	}
	return result
}

func (m *Manager) describe(target string) (AppDescribe, error) {
	names, err := m.resolveTargets(target)
	if err != nil {
		return AppDescribe{}, err
	}
	if len(names) == 0 {
		return AppDescribe{}, fmt.Errorf("no app matches %q", target)
	}
	if len(names) > 1 {
		return AppDescribe{}, fmt.Errorf("target %q resolves to %d apps; please use app name", target, len(names))
	}

	name := names[0]
	m.mu.Lock()
	mp, ok := m.apps[name]
	if !ok {
		m.mu.Unlock()
		return AppDescribe{}, fmt.Errorf("app %q not found", name)
	}
	desc := AppDescribe{
		Name:     mp.Config.Name,
		PID:      mp.PID,
		Status:   effectiveStatus(mp),
		Restarts: mp.RestartCount,
		Desired:  mp.Desired,
		Script:   mp.Config.Script,
		Args:     append([]string(nil), mp.Config.Args...),
		Cwd:      mp.Config.Cwd,
		Env:      cloneEnv(mp.Config.Env),
		LogOut:   filepath.Join(m.logsDir, fmt.Sprintf("%s-out.log", mp.Config.Name)),
		LogErr:   filepath.Join(m.logsDir, fmt.Sprintf("%s-err.log", mp.Config.Name)),
	}
	if mp.Status == "online" && !mp.StartedAt.IsZero() {
		desc.Uptime = int64(time.Since(mp.StartedAt).Seconds())
	}
	desc.Autorestart = true
	if mp.Config.Autorestart != nil {
		desc.Autorestart = *mp.Config.Autorestart
	}
	m.mu.Unlock()

	return desc, nil
}

func (m *Manager) save() (int, error) {
	m.mu.Lock()
	eco := Ecosystem{Apps: make([]AppConfig, 0, len(m.apps))}
	for _, mp := range m.apps {
		eco.Apps = append(eco.Apps, mp.Config)
	}
	m.mu.Unlock()

	path := filepath.Join(m.stateDir, "dump.json")
	b, err := json.MarshalIndent(eco, "", "  ")
	if err != nil {
		return 0, err
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return 0, err
	}
	return len(eco.Apps), nil
}

func (m *Manager) resurrect() (int, error) {
	path := filepath.Join(m.stateDir, "dump.json")
	eco, err := readEcosystem(path)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, app := range eco.Apps {
		if err := m.startApp(app); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

func (m *Manager) kill(name string) bool {
	m.mu.Lock()
	mp, ok := m.apps[name]
	if !ok || mp.Cmd == nil || mp.Cmd.Process == nil {
		if ok {
			mp.Status = "stopped"
			mp.PID = 0
		}
		m.mu.Unlock()
		return false
	}
	proc := mp.Cmd.Process
	pid := proc.Pid
	mp.Cmd = nil
	mp.PID = 0
	mp.Status = "stopped"
	m.mu.Unlock()

	killed := killProcessTree(pid)
	if !killed {
		_ = proc.Kill()
	}

	m.mu.Lock()
	if mp2, ok := m.apps[name]; ok {
		mp2.Status = "stopped"
		mp2.PID = 0
		mp2.Cmd = nil
	}
	m.mu.Unlock()
	return true
}

func killProcessTree(pid int) bool {
	if pid <= 0 {
		return false
	}

	cmd := exec.Command("taskkill", "/PID", fmt.Sprintf("%d", pid), "/T", "/F")
	if err := cmd.Run(); err != nil {
		return false
	}
	return true
}

func (m *Manager) resolveTargets(target string) ([]string, error) {
	target = strings.TrimSpace(target)

	m.mu.Lock()
	if strings.EqualFold(target, "all") {
		names := make([]string, 0, len(m.apps))
		for n := range m.apps {
			names = append(names, n)
		}
		m.mu.Unlock()
		return names, nil
	}
	if _, ok := m.apps[target]; ok {
		m.mu.Unlock()
		return []string{target}, nil
	}
	m.mu.Unlock()

	if _, err := os.Stat(target); err == nil {
		eco, err := readEcosystem(target)
		if err != nil {
			return nil, fmt.Errorf("failed to parse ecosystem file %q: %w", target, err)
		}

		set := make(map[string]struct{}, len(eco.Apps))
		for _, app := range eco.Apps {
			if app.Name != "" {
				set[app.Name] = struct{}{}
			}
		}

		m.mu.Lock()
		names := make([]string, 0, len(set))
		for name := range set {
			if _, ok := m.apps[name]; ok {
				names = append(names, name)
			}
		}
		m.mu.Unlock()

		if len(names) == 0 {
			return nil, fmt.Errorf("no managed apps from ecosystem file %q", target)
		}
		return names, nil
	}

	return nil, fmt.Errorf("no app matches %q", target)
}

func readEcosystem(path string) (Ecosystem, error) {
	file, err := os.Open(path)
	if err != nil {
		return Ecosystem{}, err
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		return Ecosystem{}, err
	}
	var eco Ecosystem
	if err := json.Unmarshal(data, &eco); err != nil {
		return Ecosystem{}, err
	}
	for i := range eco.Apps {
		if eco.Apps[i].Env == nil {
			eco.Apps[i].Env = map[string]any{}
		}
	}
	return eco, nil
}

func mergedEnv(overrides map[string]any) []string {
	env := os.Environ()
	for k, v := range overrides {
		env = append(env, fmt.Sprintf("%s=%v", k, v))
	}
	return env
}

func cloneEnv(src map[string]any) map[string]any {
	if len(src) == 0 {
		return map[string]any{}
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func effectiveStatus(mp *ManagedProc) string {
	if mp == nil {
		return "stopped"
	}
	if !mp.Desired && mp.PID == 0 {
		return "stopped"
	}
	return mp.Status
}

func ensureDirs() (string, string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}
	state := filepath.Join(home, ".winpm2")
	logs := filepath.Join(state, "logs")
	if err := os.MkdirAll(logs, 0o755); err != nil {
		return "", "", err
	}
	return state, logs, nil
}

func installStartupRunKey() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	value := fmt.Sprintf("\"%s\" daemon --autoload", exe)
	key, _, err := registry.CreateKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	return key.SetStringValue(runValueName, value)
}

func uninstallStartupRunKey() error {
	key, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	err = key.DeleteValue(runValueName)
	if err != nil && !errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) {
		return err
	}
	return nil
}

func startupStatus() (string, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return "", err
	}
	defer key.Close()
	value, _, err := key.GetStringValue(runValueName)
	if err != nil {
		return "", err
	}
	return value, nil
}

func isElevated() bool {
	token := windows.GetCurrentProcessToken()
	return token.IsElevated()
}

func trimOldStarts(starts []time.Time) []time.Time {
	cutoff := time.Now().Add(-1 * time.Minute)
	trimmed := starts[:0]
	for _, s := range starts {
		if s.After(cutoff) {
			trimmed = append(trimmed, s)
		}
	}
	return trimmed
}

func restartDelay(restartCount int) time.Duration {
	if restartCount < 1 {
		return time.Second
	}
	d := time.Second * time.Duration(1<<min(restartCount, 5))
	if d > 30*time.Second {
		return 30 * time.Second
	}
	return d
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func currentUsername() string {
	u, err := user.Current()
	if err != nil {
		return "unknown"
	}
	if u.Username == "" {
		return "unknown"
	}
	return u.Username
}
