package webui

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MogaleAlpheus/ShareNet/linux/internal/adb"
	"github.com/MogaleAlpheus/ShareNet/linux/internal/discovery"
	"github.com/MogaleAlpheus/ShareNet/linux/internal/logx"
	"github.com/MogaleAlpheus/ShareNet/linux/internal/netcfg"
	"github.com/MogaleAlpheus/ShareNet/linux/internal/settings"
	"github.com/MogaleAlpheus/ShareNet/linux/internal/stats"
	"github.com/MogaleAlpheus/ShareNet/linux/internal/tunnel"
)

type fakeController struct {
	mu sync.Mutex

	state     tunnel.State
	status    string
	lastErr   error
	transport string

	usbCalls    int
	usbSerial   string
	wifiCalls   int
	wifiHost    string
	wifiToken   string
	wifiAdapter string
	wifiFind    bool
	wifiHotspot bool

	disconnects int
	devices     []adb.Device
	adapters    []netcfg.Adapter
	phones      []discovery.Phone
	snapshot    stats.Snapshot
	logs        []logx.Entry
}

func (f *fakeController) State() (tunnel.State, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state, f.status, f.lastErr
}

func (f *fakeController) Transport() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.transport
}

func (f *fakeController) ConnectUSB(ctx context.Context, serial, token string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.usbCalls++
	f.usbSerial = serial
	f.state = tunnel.StateConnected
	return nil
}

func (f *fakeController) ConnectWiFi(ctx context.Context, host, token, adapter string, find, hotspot bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.wifiCalls++
	f.wifiHost = host
	f.wifiToken = token
	f.wifiAdapter = adapter
	f.wifiFind = find
	f.wifiHotspot = hotspot
	f.transport = "wifi"
	f.state = tunnel.StateConnected
	return nil
}

func (f *fakeController) Disconnect(ctx context.Context) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.disconnects++
	f.state = tunnel.StateStopped
}

func (f *fakeController) Devices(ctx context.Context) ([]adb.Device, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.devices, nil
}

func (f *fakeController) Adapters(ctx context.Context) ([]netcfg.Adapter, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.adapters, nil
}

func (f *fakeController) Discover(ctx context.Context, adapter string) ([]discovery.Phone, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.phones, nil
}

func (f *fakeController) Snapshot() stats.Snapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.snapshot
}

func (f *fakeController) Logs(n int) []logx.Entry {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.logs
}

func startTestServer(t *testing.T, controller *fakeController) (*Server, *settings.Store, *int) {
	t.Helper()

	store := settings.Open(t.TempDir())
	quits := 0

	server, err := Start(Options{
		Controller:      controller,
		Settings:        store,
		Version:         "9.9.9",
		Root:            true,
		EngineAvailable: true,
		OnQuit:          func() { quits++ },
		Log:             func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(server.Close)
	return server, store, &quits
}

func request(t *testing.T, method, url, token string, body string) (int, []byte) {
	t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("X-ShareNet-Token", token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, payload
}

func tokenOf(t *testing.T, url string) (string, string) {
	t.Helper()
	parsed, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	base := "http://" + parsed.URL.Host
	return base, parsed.URL.Query().Get("token")
}

func TestWindowServesPageOnlyWithToken(t *testing.T) {
	controller := &fakeController{state: tunnel.StateStopped, status: "Ready"}
	server, _, _ := startTestServer(t, controller)
	base, token := tokenOf(t, server.URL())

	if code, _ := request(t, "GET", base+"/", "", ""); code != http.StatusForbidden {
		t.Fatalf("page without a token = %d, want 403", code)
	}

	code, body := request(t, "GET", base+"/?token="+token, token, "")
	if code != http.StatusOK {
		t.Fatalf("page = %d", code)
	}
	if !strings.Contains(string(body), "ShareNet") || !strings.Contains(string(body), token) {
		t.Fatal("the page did not include the app shell and token")
	}
}

func TestStatusRejectsWrongTokenAndReportsTraffic(t *testing.T) {
	controller := &fakeController{
		state:     tunnel.StateConnected,
		status:    "Connected over Wi-Fi — 192.168.43.1",
		transport: "wifi",
		snapshot: stats.Snapshot{
			Download: 1500, Upload: 500,
			DownloadRate: 2048, UploadRate: 1024,
			PeakDownload: 4096, PeakUpload: 2048,
			Duration: 65 * time.Second,
			History:  []stats.Point{{Download: 1024, Upload: 512}},
		},
		logs: []logx.Entry{{Level: "INFO", Message: "Connected"}},
	}
	server, _, _ := startTestServer(t, controller)
	base, token := tokenOf(t, server.URL())

	if code, _ := request(t, "GET", base+"/api/status", "nope", ""); code != http.StatusForbidden {
		t.Fatalf("wrong token = %d, want 403", code)
	}

	code, body := request(t, "GET", base+"/api/status", token, "")
	if code != http.StatusOK {
		t.Fatalf("status = %d: %s", code, body)
	}

	var payload statusPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("decode: %v (%s)", err, body)
	}
	if payload.State != "Connected" || payload.Transport != "wifi" {
		t.Fatalf("payload %+v", payload)
	}
	if payload.Traffic.DownloadRate != 2048 || len(payload.Traffic.History) != 1 {
		t.Fatalf("traffic %+v", payload.Traffic)
	}
	if payload.Version != "9.9.9" || !payload.Root || !payload.Engine {
		t.Fatalf("badges %+v", payload)
	}
	if len(payload.Logs) != 1 || payload.Logs[0].Message != "Connected" {
		t.Fatalf("logs %+v", payload.Logs)
	}
}

func TestConnectUsesSavedPasswordAndSavesSettings(t *testing.T) {
	controller := &fakeController{}
	server, store, _ := startTestServer(t, controller)
	base, token := tokenOf(t, server.URL())

	if err := store.Update(func(next *settings.Settings) { next.Password = "7K4P9Q2M" }); err != nil {
		t.Fatal(err)
	}

	code, body := request(t, "POST", base+"/api/connect", token,
		`{"mode":"wifi","host":"192.168.43.1","adapter":"wlp2s0","hotspot":true}`)
	if code != http.StatusAccepted {
		t.Fatalf("connect = %d: %s", code, body)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		controller.mu.Lock()
		calls := controller.wifiCalls
		controller.mu.Unlock()
		if calls > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	controller.mu.Lock()
	defer controller.mu.Unlock()
	if controller.wifiCalls != 1 {
		t.Fatalf("wifi connects = %d, want 1", controller.wifiCalls)
	}
	if controller.wifiToken != "7K4P9Q2M" {
		t.Fatalf("token = %q, want the saved password", controller.wifiToken)
	}
	if !controller.wifiHotspot || controller.wifiHost != "192.168.43.1" {
		t.Fatalf("connect args %+v", controller)
	}

	current := store.Current()
	if current.Mode != "wifi" || current.WifiHost != "192.168.43.1" || !current.Hotspot {
		t.Fatalf("saved settings %+v", current)
	}
}

func TestConnectRejectsUnknownMode(t *testing.T) {
	controller := &fakeController{}
	server, _, _ := startTestServer(t, controller)
	base, token := tokenOf(t, server.URL())

	code, _ := request(t, "POST", base+"/api/connect", token, `{"mode":"carrier-pigeon"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("bad mode = %d, want 400", code)
	}
}

func TestUSBConnectAndDisconnect(t *testing.T) {
	controller := &fakeController{}
	server, store, _ := startTestServer(t, controller)
	base, token := tokenOf(t, server.URL())

	if code, body := request(t, "POST", base+"/api/connect", token,
		`{"mode":"usb","serial":"R58M123ABC"}`); code != http.StatusAccepted {
		t.Fatalf("connect = %d: %s", code, body)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		controller.mu.Lock()
		calls := controller.usbCalls
		controller.mu.Unlock()
		if calls > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	controller.mu.Lock()
	if controller.usbSerial != "R58M123ABC" {
		t.Fatalf("serial = %q", controller.usbSerial)
	}
	controller.mu.Unlock()

	if current := store.Current(); current.LastSerial != "R58M123ABC" {
		t.Fatalf("remembered serial = %q", current.LastSerial)
	}

	if code, _ := request(t, "POST", base+"/api/disconnect", token, "{}"); code != http.StatusAccepted {
		t.Fatalf("disconnect = %d", code)
	}

	deadline = time.Now().Add(2 * time.Second)
	dispatched := false
	for time.Now().Before(deadline) {
		controller.mu.Lock()
		count := controller.disconnects
		controller.mu.Unlock()
		if count > 0 {
			dispatched = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !dispatched {
		t.Fatal("disconnect was not dispatched")
	}
}

func TestDevicesAdaptersAndFind(t *testing.T) {
	controller := &fakeController{
		devices: []adb.Device{{Serial: "R58M", State: "device", Model: "Pixel 8"}},
		adapters: []netcfg.Adapter{
			{Name: "wlp2s0", Address: "192.168.1.5", Wireless: true},
		},
		phones: []discovery.Phone{{IP: net.ParseIP("192.168.1.20"), Port: 1080, Model: "Pixel 8"}},
	}
	server, _, _ := startTestServer(t, controller)
	base, token := tokenOf(t, server.URL())

	_, body := request(t, "POST", base+"/api/devices", token, "{}")
	if !strings.Contains(string(body), "Pixel 8") || !strings.Contains(string(body), `"usable":true`) {
		t.Fatalf("devices: %s", body)
	}

	_, body = request(t, "GET", base+"/api/adapters", token, "")
	if !strings.Contains(string(body), "wlp2s0") || !strings.Contains(string(body), `"wireless":true`) {
		t.Fatalf("adapters: %s", body)
	}

	_, body = request(t, "POST", base+"/api/find", token, `{"adapter":"wlp2s0"}`)
	if !strings.Contains(string(body), "192.168.1.20") {
		t.Fatalf("find: %s", body)
	}
}

func TestSettingsAndQuit(t *testing.T) {
	controller := &fakeController{}
	server, store, quits := startTestServer(t, controller)
	base, token := tokenOf(t, server.URL())

	code, body := request(t, "POST", base+"/api/settings", token,
		`{"mode":"wifi","wifiHost":"10.0.0.9","adapterName":"wlp2s0","lastSerial":"X","hotspot":true}`)
	if code != http.StatusOK {
		t.Fatalf("settings = %d: %s", code, body)
	}
	current := store.Current()
	if current.Mode != "wifi" || current.WifiHost != "10.0.0.9" || !current.Hotspot {
		t.Fatalf("stored %+v", current)
	}

	if code, _ := request(t, "POST", base+"/api/quit", token, "{}"); code != http.StatusOK {
		t.Fatalf("quit = %d", code)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if *quits > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("quit callback did not run")
}
