// Package webui serves the client's window over loopback: the Linux equivalent
// of the Windows WPF window, with the same panels (connection controls, live
// traffic, activity log) and the same remembered settings.
//
// It binds 127.0.0.1 only and every request needs the random session token, so
// another local program cannot drive the tunnel through the browser.
package webui

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/MogaleAlpheus/ShareNet/linux/internal/adb"
	"github.com/MogaleAlpheus/ShareNet/linux/internal/discovery"
	"github.com/MogaleAlpheus/ShareNet/linux/internal/logx"
	"github.com/MogaleAlpheus/ShareNet/linux/internal/netcfg"
	"github.com/MogaleAlpheus/ShareNet/linux/internal/settings"
	"github.com/MogaleAlpheus/ShareNet/linux/internal/stats"
	"github.com/MogaleAlpheus/ShareNet/linux/internal/tunnel"
)

//go:embed page.html
var pageHTML string

// Controller is the tunnel surface the window needs.
type Controller interface {
	State() (tunnel.State, string, error)
	Transport() string
	ConnectUSB(ctx context.Context, serial, token string) error
	ConnectWiFi(ctx context.Context, host, token, adapter string, find, hotspot bool) error
	Disconnect(ctx context.Context)
	Devices(ctx context.Context) ([]adb.Device, error)
	Adapters(ctx context.Context) ([]netcfg.Adapter, error)
	Discover(ctx context.Context, adapter string) ([]discovery.Phone, error)
	Snapshot() stats.Snapshot
	Logs(n int) []logx.Entry
}

// Options configures the window server.
type Options struct {
	Controller Controller
	Settings   *settings.Store
	Version    string
	// Context bounds connect and disconnect work; cancelled on shutdown.
	Context context.Context
	// Root and EngineAvailable drive the footer badges.
	Root             bool
	EngineAvailable  bool
	HotspotAvailable bool
	// OnQuit is called when the window's Quit button is pressed.
	OnQuit func()
	Log    func(string, ...any)
}

// Server is a running window.
type Server struct {
	options  Options
	token    string
	listener net.Listener
	http     *http.Server
	url      string
}

// Start listens on a random loopback port and serves the window.
func Start(options Options) (*Server, error) {
	if options.Controller == nil {
		return nil, fmt.Errorf("the window needs a controller")
	}
	if options.Context == nil {
		options.Context = context.Background()
	}
	if options.Log == nil {
		options.Log = func(string, ...any) {}
	}
	if options.Settings == nil {
		options.Settings = settings.Load()
	}

	token, err := sessionToken()
	if err != nil {
		return nil, err
	}

	server := &Server{options: options, token: token}

	mux := http.NewServeMux()
	mux.HandleFunc("/", server.index)
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/api/status", server.auth(server.status))
	mux.HandleFunc("/api/connect", server.auth(server.connect))
	mux.HandleFunc("/api/disconnect", server.auth(server.disconnect))
	mux.HandleFunc("/api/devices", server.auth(server.devices))
	mux.HandleFunc("/api/find", server.auth(server.find))
	mux.HandleFunc("/api/adapters", server.auth(server.adapters))
	mux.HandleFunc("/api/settings", server.auth(server.saveSettings))
	mux.HandleFunc("/api/quit", server.auth(server.quit))

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("could not open the window's local port: %w", err)
	}

	server.listener = listener
	server.url = fmt.Sprintf("http://%s/?token=%s", listener.Addr().String(), token)
	server.http = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		if err := server.http.Serve(listener); err != nil && err != http.ErrServerClosed {
			options.Log("Window server stopped: %v", err)
		}
	}()

	return server, nil
}

// URL is the address to open, token included.
func (s *Server) URL() string { return s.url }

// Close stops serving.
func (s *Server) Close() {
	if s == nil || s.http == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = s.http.Shutdown(ctx)
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		supplied := r.Header.Get("X-ShareNet-Token")
		if supplied == "" {
			supplied = r.URL.Query().Get("token")
		}
		if subtle.ConstantTimeCompare([]byte(supplied), []byte(s.token)) != 1 {
			http.Error(w, "forbidden: open the address printed in the terminal", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

func (s *Server) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("token")), []byte(s.token)) != 1 {
		http.Error(w, "forbidden: open the address printed in the terminal", http.StatusForbidden)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy",
		"default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; img-src data:; connect-src 'self'")
	_, _ = w.Write([]byte(strings.ReplaceAll(pageHTML, "{{TOKEN}}", s.token)))
}

type trafficPayload struct {
	Download     int64         `json:"download"`
	Upload       int64         `json:"upload"`
	DownloadRate float64       `json:"downloadRate"`
	UploadRate   float64       `json:"uploadRate"`
	PeakDownload float64       `json:"peakDownload"`
	PeakUpload   float64       `json:"peakUpload"`
	Duration     float64       `json:"duration"`
	History      []stats.Point `json:"history"`
}

type statusPayload struct {
	State       string          `json:"state"`
	Status      string          `json:"status"`
	Error       string          `json:"error,omitempty"`
	Transport   string          `json:"transport"`
	Traffic     trafficPayload  `json:"traffic"`
	Settings    settingsPayload `json:"settings"`
	Logs        []logx.Entry    `json:"logs"`
	Version     string          `json:"version"`
	Root        bool            `json:"root"`
	Engine      bool            `json:"engine"`
	Hotspot     bool            `json:"hotspot"`
	PasswordSet bool            `json:"passwordSet"`
}

type settingsPayload struct {
	Mode        string `json:"mode"`
	WifiHost    string `json:"wifiHost"`
	AdapterName string `json:"adapterName"`
	LastSerial  string `json:"lastSerial"`
	Hotspot     bool   `json:"hotspot"`
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	state, status, lastErr := s.options.Controller.State()
	snapshot := s.options.Controller.Snapshot()
	current := s.options.Settings.Current()

	payload := statusPayload{
		State:     state.String(),
		Status:    status,
		Transport: s.options.Controller.Transport(),
		Traffic: trafficPayload{
			Download:     snapshot.Download,
			Upload:       snapshot.Upload,
			DownloadRate: snapshot.DownloadRate,
			UploadRate:   snapshot.UploadRate,
			PeakDownload: snapshot.PeakDownload,
			PeakUpload:   snapshot.PeakUpload,
			Duration:     snapshot.Duration.Seconds(),
			History:      snapshot.History,
		},
		Settings: settingsPayload{
			Mode:        current.Mode,
			WifiHost:    current.WifiHost,
			AdapterName: current.AdapterName,
			LastSerial:  current.LastSerial,
			Hotspot:     current.Hotspot,
		},
		Logs:        s.options.Controller.Logs(150),
		Version:     s.options.Version,
		Root:        s.options.Root,
		Engine:      s.options.EngineAvailable,
		Hotspot:     s.options.HotspotAvailable,
		PasswordSet: current.Password != "",
	}
	if lastErr != nil {
		payload.Error = lastErr.Error()
	}
	if payload.Traffic.History == nil {
		payload.Traffic.History = []stats.Point{}
	}
	if payload.Logs == nil {
		payload.Logs = []logx.Entry{}
	}

	writeJSON(w, http.StatusOK, payload)
}

type connectRequest struct {
	Mode     string `json:"mode"`
	Serial   string `json:"serial"`
	Host     string `json:"host"`
	Password string `json:"password"`
	Adapter  string `json:"adapter"`
	Find     bool   `json:"find"`
	Hotspot  bool   `json:"hotspot"`
}

func (s *Server) connect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	var request connectRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&request); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	mode := strings.ToLower(strings.TrimSpace(request.Mode))
	if mode != "usb" && mode != "wifi" {
		http.Error(w, "mode must be usb or wifi", http.StatusBadRequest)
		return
	}

	saved := s.options.Settings.Current()
	password := request.Password
	if password == "" && mode == "wifi" {
		password = saved.Password
	}

	// The password is saved as soon as it is used, like the Windows app, so the
	// next launch does not ask again.
	if mode == "wifi" && password != "" {
		_ = s.options.Settings.Update(func(next *settings.Settings) {
			next.Mode = mode
			next.Password = password
			next.WifiHost = request.Host
			next.AdapterName = request.Adapter
			next.Hotspot = request.Hotspot
		})
	}

	writeJSON(w, http.StatusAccepted, map[string]string{"status": "connecting"})

	ctx, cancel := context.WithTimeout(s.options.Context, 2*time.Minute)
	defer cancel()

	var err error
	if mode == "usb" {
		err = s.options.Controller.ConnectUSB(ctx, strings.TrimSpace(request.Serial), "")
	} else {
		err = s.options.Controller.ConnectWiFi(
			ctx,
			strings.TrimSpace(request.Host),
			password,
			strings.TrimSpace(request.Adapter),
			request.Find,
			request.Hotspot,
		)
	}
	if err != nil {
		s.options.Log("Window connect failed: %v", err)
		return
	}

	_ = s.options.Settings.Update(func(next *settings.Settings) {
		next.Mode = mode
		if mode == "usb" {
			next.LastSerial = strings.TrimSpace(request.Serial)
			return
		}
		next.WifiHost = strings.TrimSpace(request.Host)
		next.AdapterName = strings.TrimSpace(request.Adapter)
		next.Hotspot = request.Hotspot
	})
}

func (s *Server) disconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	writeJSON(w, http.StatusAccepted, map[string]string{"status": "disconnecting"})
	go func() {
		ctx, cancel := context.WithTimeout(s.options.Context, 30*time.Second)
		defer cancel()
		s.options.Controller.Disconnect(ctx)
	}()
}

func (s *Server) devices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	ctx, cancel := context.WithTimeout(s.options.Context, 25*time.Second)
	defer cancel()

	type device struct {
		Serial  string `json:"serial"`
		Model   string `json:"model"`
		State   string `json:"state"`
		Display string `json:"display"`
		Usable  bool   `json:"usable"`
	}

	devices, err := s.options.Controller.Devices(ctx)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"devices": []device{}, "error": err.Error()})
		return
	}

	list := make([]device, 0, len(devices))
	for _, item := range devices {
		list = append(list, device{
			Serial:  item.Serial,
			Model:   item.Model,
			State:   item.State,
			Display: item.DisplayName(),
			Usable:  item.Usable(),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": list})
}

func (s *Server) find(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	var request struct {
		Adapter string `json:"adapter"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&request)

	ctx, cancel := context.WithTimeout(s.options.Context, 30*time.Second)
	defer cancel()

	phones, err := s.options.Controller.Discover(ctx, strings.TrimSpace(request.Adapter))
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"phones": []any{}, "error": err.Error()})
		return
	}

	type phone struct {
		IP      string `json:"ip"`
		Port    int    `json:"port"`
		Model   string `json:"model"`
		Display string `json:"display"`
	}

	list := make([]phone, 0, len(phones))
	for _, item := range phones {
		list = append(list, phone{
			IP:      item.IP.String(),
			Port:    item.Port,
			Model:   item.Model,
			Display: item.String(),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"phones": list})
}

func (s *Server) adapters(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}

	ctx, cancel := context.WithTimeout(s.options.Context, 10*time.Second)
	defer cancel()

	adapters, err := s.options.Controller.Adapters(ctx)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"adapters": []any{}, "error": err.Error()})
		return
	}

	type adapter struct {
		Name     string `json:"name"`
		Address  string `json:"address"`
		Prefix   string `json:"prefix"`
		Gateway  string `json:"gateway"`
		Wireless bool   `json:"wireless"`
		Display  string `json:"display"`
	}

	list := make([]adapter, 0, len(adapters))
	for _, item := range adapters {
		list = append(list, adapter{
			Name:     item.Name,
			Address:  item.Address,
			Prefix:   item.Prefix,
			Gateway:  item.Gateway,
			Wireless: item.Wireless,
			Display:  item.Display(),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"adapters": list})
}

func (s *Server) saveSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}

	var request settingsPayload
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&request); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}

	err := s.options.Settings.Update(func(next *settings.Settings) {
		if request.Mode == "usb" || request.Mode == "wifi" {
			next.Mode = request.Mode
		}
		next.WifiHost = request.WifiHost
		next.AdapterName = request.AdapterName
		next.LastSerial = request.LastSerial
		next.Hotspot = request.Hotspot
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "saved"})
}

func (s *Server) quit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "quitting"})
	if s.options.OnQuit != nil {
		go func() {
			time.Sleep(150 * time.Millisecond)
			s.options.OnQuit()
		}()
	}
}

func writeJSON(w http.ResponseWriter, code int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(payload)
}

func methodNotAllowed(w http.ResponseWriter) {
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

func sessionToken() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("could not create the window token: %w", err)
	}
	return hex.EncodeToString(raw), nil
}
