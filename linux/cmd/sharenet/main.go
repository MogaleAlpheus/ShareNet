// Command sharenet shares a phone's VPN with this Linux machine as a
// transparent full-system tunnel.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/MogaleAlpheus/ShareNet/linux/internal/adb"
	"github.com/MogaleAlpheus/ShareNet/linux/internal/engine"
	"github.com/MogaleAlpheus/ShareNet/linux/internal/hotspot"
	"github.com/MogaleAlpheus/ShareNet/linux/internal/instance"
	"github.com/MogaleAlpheus/ShareNet/linux/internal/logx"
	"github.com/MogaleAlpheus/ShareNet/linux/internal/netcfg"
	"github.com/MogaleAlpheus/ShareNet/linux/internal/notify"
	"github.com/MogaleAlpheus/ShareNet/linux/internal/session"
	"github.com/MogaleAlpheus/ShareNet/linux/internal/settings"
	"github.com/MogaleAlpheus/ShareNet/linux/internal/stats"
	"github.com/MogaleAlpheus/ShareNet/linux/internal/tunnel"
	"github.com/MogaleAlpheus/ShareNet/linux/internal/webui"
)

// Version is set at build time with -ldflags "-X main.Version=…".
var Version = "0.1.0"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "\nError: %v\n", err)
		os.Exit(1)
	}
}

type globals struct {
	verbose    bool
	logFile    string
	enginePath string
	noNotify   bool
	help       bool
	version    bool
}

func run(args []string) error {
	options, rest, err := parseGlobals(args)
	if err != nil {
		return err
	}
	if options.version {
		fmt.Printf("ShareNet Linux %s\n", Version)
		return nil
	}
	if options.help || len(rest) == 0 {
		printUsage()
		return nil
	}

	command, commandArgs := rest[0], rest[1:]
	if command == "help" || command == "-h" || command == "--help" {
		printUsage()
		return nil
	}

	logger, err := logx.New(options.verbose, options.logFile)
	if err != nil {
		return err
	}
	defer logger.Close()

	switch command {
	case "connect":
		return runConnect(logger, options, commandArgs)
	case "ui", "window":
		return runUI(logger, options, commandArgs)
	case "devices":
		return runDevices()
	case "doctor":
		return runDoctor(options)
	case "cleanup":
		return runCleanup(logger)
	default:
		return fmt.Errorf("unknown command %q. Run \"sharenet help\"", command)
	}
}

func parseGlobals(args []string) (globals, []string, error) {
	var options globals
	rest := make([]string, 0, len(args))

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--":
			rest = append(rest, args[i+1:]...)
			return options, rest, nil
		case arg == "-v" || arg == "--verbose":
			options.verbose = true
		case arg == "--version":
			options.version = true
		case arg == "-h" || arg == "--help":
			options.help = true
		case arg == "--no-notify":
			options.noNotify = true
		case arg == "--log":
			if i+1 >= len(args) {
				return options, nil, errors.New("--log needs a file path")
			}
			i++
			options.logFile = args[i]
		case strings.HasPrefix(arg, "--log="):
			options.logFile = strings.TrimPrefix(arg, "--log=")
		case arg == "--engine":
			if i+1 >= len(args) {
				return options, nil, errors.New("--engine needs a path")
			}
			i++
			options.enginePath = args[i]
		case strings.HasPrefix(arg, "--engine="):
			options.enginePath = strings.TrimPrefix(arg, "--engine=")
		default:
			rest = append(rest, arg)
		}
	}

	return options, rest, nil
}

// connectTarget is one resolved connection attempt.
type connectTarget struct {
	mode     string
	serial   string
	host     string
	token    string
	adapter  string
	find     bool
	hotspot  bool
	password string
}

func (t connectTarget) modeLabel() string {
	if t.mode == "wifi" {
		return "Wi-Fi"
	}
	return "USB"
}

func (t connectTarget) run(ctx context.Context, controller *tunnel.Controller) error {
	if t.mode == "wifi" {
		return controller.ConnectWiFi(ctx, t.host, t.token, t.adapter, t.find, t.hotspot)
	}
	return controller.ConnectUSB(ctx, t.serial, "")
}

func (t connectTarget) save(store *settings.Store) {
	_ = store.Update(func(next *settings.Settings) {
		next.Mode = t.mode
		if t.mode == "usb" {
			next.LastSerial = t.serial
			return
		}
		next.WifiHost = t.host
		next.AdapterName = t.adapter
		next.Hotspot = t.hotspot
		if t.token != "" {
			next.Password = t.token
		}
	})
}

func newController(logger *logx.Logger, options globals, counter *stats.Stats) *tunnel.Controller {
	controller := tunnel.New(logger, counter, options.enginePath)
	if !options.noNotify {
		invoker := session.Invoker()
		controller.SetNotifier(func(title, body string, urgent bool) {
			if options.noNotify {
				return
			}
			notify.SendAs(invoker, title, body, urgent)
		})
	}
	return controller
}

func runConnect(logger *logx.Logger, options globals, args []string) error {
	flags := flag.NewFlagSet("connect", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)

	usb := flags.Bool("usb", false, "connect over USB with adb")
	serial := flags.String("serial", "", "adb serial when several phones are attached")
	wifi := flags.String("wifi", "", "the phone's IPv4 address on this network")
	token := flags.String("token", "", "the password shown in the phone app, if one is set")
	find := flags.Bool("find", false, "search the local network for the phone")
	adapter := flags.String("adapter", "", "network interface to search on with --find")
	hotspotJoin := flags.Bool("hotspot", false, "join the phone's own hotspot first")

	if err := flags.Parse(args); err != nil {
		return err
	}

	if *usb && (*wifi != "" || *find || *hotspotJoin) {
		return errors.New("choose either USB or Wi-Fi, not both")
	}
	if positional := flags.Arg(0); positional != "" {
		if *usb || *serial != "" {
			if *serial == "" {
				*serial = positional
			}
		} else if *wifi == "" {
			*wifi = positional
		}
	}

	if *adapter != "" && !*find && !*hotspotJoin {
		return errors.New("--adapter only applies with --find or --hotspot")
	}

	lock, err := instance.Acquire("session")
	if err != nil {
		return err
	}
	defer lock.Release()

	store := settings.Load()
	saved := store.Current()

	mode := saved.Mode
	switch {
	case *usb || *serial != "":
		mode = "usb"
	case *wifi != "" || *find || *hotspotJoin:
		mode = "wifi"
	}

	if mode == "wifi" && *wifi == "" && saved.WifiHost != "" && saved.Mode == "wifi" && !*find {
		*wifi = saved.WifiHost
	}
	if *serial == "" && mode == "usb" {
		*serial = saved.LastSerial
	}
	if *adapter == "" {
		*adapter = saved.AdapterName
	}
	if *token == "" {
		*token = saved.Password
	}

	target := connectTarget{
		mode:    mode,
		serial:  *serial,
		host:    *wifi,
		token:   *token,
		adapter: *adapter,
		find:    *find,
		hotspot: *hotspotJoin || (mode == "wifi" && *wifi == "" && saved.Hotspot && *token != ""),
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	counter := stats.New()
	controller := newController(logger, options, counter)

	// With no transport argument, prefer USB when a phone is attached, exactly
	// like the app: plug in the cable and press Connect.
	if !*usb && *wifi == "" && !*find && !*hotspotJoin {
		devices, err := controller.Devices(ctx)
		if err != nil {
			logger.Warn("USB is not available: %v", err)
		}
		usable := false
		for _, device := range devices {
			if device.Usable() {
				usable = true
				break
			}
		}
		if !usable {
			if saved.Mode == "wifi" {
				if saved.Hotspot && *token != "" {
					target.mode = "wifi"
					target.hotspot = true
				} else if saved.WifiHost != "" {
					target.mode = "wifi"
					target.host = saved.WifiHost
				} else {
					return errors.New(
						"no phone found over USB. Plug in the phone with USB debugging enabled, " +
							"or connect over Wi-Fi with --wifi <phone-ip>, --find, or --hotspot",
					)
				}
			} else {
				return errors.New(
					"no phone found over USB. Plug in the phone with USB debugging enabled, " +
						"or connect over Wi-Fi with --wifi <phone-ip>, --find, or --hotspot",
				)
			}
		}
	}

	logger.Info("Starting ShareNet over %s", target.modeLabel())
	if err := target.run(ctx, controller); err != nil {
		return err
	}
	target.save(store)

	logger.Info("Press Ctrl+C to disconnect")
	<-ctx.Done()

	disconnectCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	controller.Disconnect(disconnectCtx)

	snapshot := counter.Snapshot()
	logger.Info(
		"Session: down %s, up %s, %s",
		stats.FormatBytes(snapshot.Download),
		stats.FormatBytes(snapshot.Upload),
		formatDuration(snapshot.Duration),
	)
	return nil
}

func runUI(logger *logx.Logger, options globals, args []string) error {
	flags := flag.NewFlagSet("ui", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)

	autoconnect := flags.Bool("autoconnect", false, "connect as soon as the window opens")
	noBrowser := flags.Bool("no-browser", false, "do not open a browser automatically")
	usb := flags.Bool("usb", false, "autoconnect over USB")
	wifi := flags.Bool("wifi", false, "autoconnect over Wi-Fi")

	if err := flags.Parse(args); err != nil {
		return err
	}

	lock, err := instance.Acquire("session")
	if err != nil {
		return err
	}
	defer lock.Release()

	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, quit := context.WithCancel(signalCtx)
	defer quit()

	store := settings.Load()
	counter := stats.New()
	controller := newController(logger, options, counter)

	if netcfg.IsRoot() {
		cleanupCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		controller.CleanupStale(cleanupCtx)
		cancel()
	}

	_, engineErr := engine.Find(options.enginePath)

	server, err := webui.Start(webui.Options{
		Controller:       controller,
		Settings:         store,
		Version:          Version,
		Context:          ctx,
		Root:             netcfg.IsRoot(),
		EngineAvailable:  engineErr == nil,
		HotspotAvailable: hotspot.Available(),
		OnQuit:           quit,
		Log:              logger.Info,
	})
	if err != nil {
		return err
	}
	defer server.Close()

	fmt.Printf("ShareNet %s is running.\n", Version)
	fmt.Printf("Open the window: %s\n", server.URL())
	fmt.Printf("Settings: %s\n", settingsPath(store))
	if engineErr != nil {
		fmt.Printf("Note: %v\n", engineErr)
	}
	fmt.Println("Press Ctrl+C to stop and restore this computer's networking.")

	if !*noBrowser && os.Getenv("SHARENET_NO_BROWSER") != "1" {
		openBrowser(server.URL(), logger)
	}

	if *autoconnect {
		saved := store.Current()
		go func() {
			connectCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			defer cancel()

			target := connectTarget{
				mode:    saved.Mode,
				serial:  saved.LastSerial,
				host:    saved.WifiHost,
				token:   saved.Password,
				adapter: saved.AdapterName,
				hotspot: saved.Hotspot,
			}
			if *usb {
				target.mode, target.hotspot = "usb", false
			}
			if *wifi {
				target.mode = "wifi"
			}

			logger.Info("Autoconnect over %s", target.modeLabel())
			if err := target.run(connectCtx, controller); err != nil {
				logger.Warn("Autoconnect failed: %v", err)
				return
			}
			target.save(store)
		}()
	}

	<-ctx.Done()

	disconnectCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	controller.Disconnect(disconnectCtx)
	logger.Info("ShareNet stopped")
	return nil
}

func openBrowser(url string, logger *logx.Logger) {
	// Elevated through sudo or pkexec: the browser belongs to the desktop user,
	// not to root's empty session.
	if invoker := session.Invoker(); invoker != nil {
		if err := session.RunAs(invoker, "xdg-open", url); err == nil {
			return
		}
		logger.Warn("Could not open a browser as %s; open the address above", invoker.Name)
		return
	}

	openers := [][]string{
		{"xdg-open", url},
		{"gio", "open", url},
		{"sensible-browser", url},
	}
	for _, command := range openers {
		if _, err := exec.LookPath(command[0]); err != nil {
			continue
		}
		if err := exec.Command(command[0], command[1:]...).Start(); err == nil {
			return
		}
	}
	logger.Warn("Could not open a browser automatically; open the address above")
}

func settingsPath(store *settings.Store) string {
	if path := store.Path(); path != "" {
		return path
	}
	return "(settings cannot be saved)"
}

func runDevices() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	path, err := adb.Find()
	if err != nil {
		return err
	}

	manager := adb.New(path)
	if err := manager.EnsureServer(ctx); err != nil {
		return err
	}

	devices, err := manager.Devices(ctx)
	if err != nil {
		return err
	}
	if len(devices) == 0 {
		fmt.Println("No Android devices are attached over USB.")
		fmt.Println("Connect the phone, or share over Wi-Fi with: sudo sharenet connect --find")
		return nil
	}

	fmt.Println("Android devices:")
	for _, device := range devices {
		fmt.Printf("  %s\n", device.DisplayName())
	}
	return nil
}

func runDoctor(options globals) error {
	type check struct {
		name     string
		err      error
		optional bool
	}

	checks := make([]check, 0, 10)
	add := func(name string, err error) { checks = append(checks, check{name: name, err: err}) }
	addOptional := func(name string, err error) {
		checks = append(checks, check{name: name, err: err, optional: true})
	}

	if netcfg.IsRoot() {
		add("root privileges", nil)
	} else {
		add("root privileges", errors.New("run ShareNet with sudo"))
	}

	if _, err := os.Stat("/dev/net/tun"); err != nil {
		add("/dev/net/tun", errors.New("/dev/net/tun is missing — sudo modprobe tun"))
	} else {
		add("/dev/net/tun", nil)
	}

	if _, err := exec.LookPath("ip"); err != nil {
		add("iproute2 (ip)", errors.New("sudo dnf install iproute"))
	} else {
		add("iproute2 (ip)", nil)
	}

	if path, err := engine.Find(options.enginePath); err != nil {
		add("tun2socks engine", err)
	} else if version, versionErr := engine.Version(path); versionErr != nil {
		add("tun2socks engine ("+path+")", versionErr)
	} else {
		add("tun2socks engine ("+path+", "+version+")", nil)
		if version != engine.PinnedVersion {
			addOptional("tun2socks version", fmt.Errorf(
				"ShareNet is built for %s, this engine reports %s — refresh it with scripts/fetch-engine.sh",
				engine.PinnedVersion, version))
		}
	}

	if path, err := adb.Find(); err != nil {
		add("adb (USB transport)", err)
	} else {
		add("adb ("+path+")", nil)
	}

	if _, err := exec.LookPath("resolvectl"); err != nil {
		add("resolvectl", errors.New("sudo dnf install systemd-resolved"))
	} else if err := exec.Command("systemctl", "is-active", "--quiet", "systemd-resolved").Run(); err != nil {
		add("systemd-resolved", errors.New("sudo systemctl enable --now systemd-resolved"))
	} else {
		add("systemd-resolved", nil)
	}

	if hotspot.Available() {
		addOptional("nmcli (join the phone's hotspot)", nil)
	} else {
		addOptional("nmcli (join the phone's hotspot)", errors.New("optional: sudo dnf install NetworkManager"))
	}

	if notify.Available() {
		addOptional("notify-send (desktop notifications)", nil)
	} else {
		addOptional("notify-send (desktop notifications)", errors.New("optional: sudo dnf install libnotify"))
	}

	store := settings.Load()
	if path := store.Path(); path != "" {
		addOptional("settings file", nil)
	} else {
		addOptional("settings file", errors.New("optional: set XDG_CONFIG_HOME or HOME to remember your choices"))
	}

	fmt.Printf("ShareNet Linux %s — system check\n\n", Version)

	failed := 0
	for _, item := range checks {
		switch {
		case item.err == nil:
			fmt.Printf("  ok    %s\n", item.name)
		case item.optional:
			fmt.Printf("  --    %s\n        %v\n", item.name, item.err)
		default:
			failed++
			fmt.Printf("  FAIL  %s\n        %v\n", item.name, item.err)
		}
	}

	fmt.Println()
	if failed > 0 {
		return fmt.Errorf("%d check(s) need attention", failed)
	}
	fmt.Println("Everything ShareNet needs is in place.")
	return nil
}

func runCleanup(logger *logx.Logger) error {
	lock, err := instance.Acquire("session")
	if err != nil {
		return err
	}
	defer lock.Release()

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	if netcfg.IsRoot() {
		netcfg.Cleanup(ctx, logger.Info)
	} else {
		logger.Warn("Not running as root, so the virtual adapter cannot be removed. Re-run with: sudo sharenet cleanup")
	}

	if path, err := adb.Find(); err == nil {
		adb.New(path).RemoveStaleForwards(ctx)
		logger.Info("Removed leftover adb forwards")
	}

	hotspot.Leave(ctx, logger.Info)

	logger.Info("Cleanup complete")
	return nil
}

func formatDuration(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}

	d = d.Round(time.Second)
	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60
	seconds := int(d.Seconds()) % 60

	switch {
	case hours > 0:
		return fmt.Sprintf("%dh %dm %ds", hours, minutes, seconds)
	case minutes > 0:
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	default:
		return fmt.Sprintf("%ds", seconds)
	}
}

func printUsage() {
	fmt.Print(`ShareNet Linux — share your phone's VPN with this computer.

The phone runs the ShareNet Android app; this client turns the phone's VPN into
a transparent system tunnel, exactly like the Windows app does.

Usage:
  sudo sharenet ui                    open the window and control the tunnel
  sudo sharenet connect [options]     connect and stay in the foreground
  sharenet devices                    list phones attached over USB
  sudo sharenet doctor                check the system requirements
  sudo sharenet cleanup               remove leftovers from a crashed run

Window options:
  --autoconnect         connect as soon as the window opens
  --usb, --wifi         force the transport for --autoconnect
  --no-browser          print the address instead of opening a browser

Connect options:
  --usb                 use USB (adb) instead of Wi-Fi
  --serial SERIAL       pick a phone when several are attached
  --wifi IP             the phone's IPv4 address on this network
  --find                search this network for the phone (Wi-Fi)
  --hotspot             join the access point the phone opens (Wi-Fi)
  --adapter IFACE       interface to search or join on (default: default route)
  --token PASSWORD      the password shown in the phone app, if one is set

Settings are remembered between runs, including the Wi-Fi password, the last
phone, and the adapter. Pass --token to override the saved password.

Examples:
  sudo sharenet ui                            # window, USB by default
  sudo sharenet ui --autoconnect              # connect on launch
  sudo sharenet connect                       # USB when a phone is plugged in
  sudo sharenet connect --hotspot --token 7K4P-9Q2M
  sudo sharenet connect --wifi 192.168.1.20
  sudo sharenet connect --find
  sudo sharenet connect --usb --serial R58M123ABC --token 7K4P-9Q2M

Global options:
  -v, --verbose         show engine and per-connection details
  --engine PATH         path to the tun2socks binary
  --log FILE            also write the log to FILE
  --no-notify           do not show desktop notifications
  --version             print the version
`)
}
