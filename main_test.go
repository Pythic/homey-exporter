package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// fakeHomey serves the testdata fixtures and counts requests. Paths in fail return HTTP 500.
func fakeHomey(t *testing.T, fail ...string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	files := map[string]string{
		"/api/manager/devices/device/": "devices.json",
		"/api/manager/zones/zone/":     "zones.json",
		"/api/manager/logic/variable/": "variables.json",
		"/api/manager/system/":         "system.json",
		"/api/manager/system/memory":   "memory.json",
		"/api/manager/system/storage":  "storage.json",
	}
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("Authorization") != "Bearer secret" {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		for _, f := range fail {
			if r.URL.Path == f {
				http.Error(w, "boom", http.StatusInternalServerError)
				return
			}
		}
		name, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		http.ServeFile(w, r, filepath.Join("testdata", name))
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenFile, []byte("from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgFile := filepath.Join(dir, "cfg.json")
	if err := os.WriteFile(cfgFile, []byte(`{"homey_url":"http://homey/","token_file":"`+tokenFile+`","cache_seconds":3}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"HOMEY_URL", "HOMEY_TOKEN", "HOMEY_TOKEN_FILE", "HOMEY_EXPORTER_LISTEN"} {
		t.Setenv(k, "")
	}
	c, err := LoadConfig(cfgFile)
	if err != nil {
		t.Fatal(err)
	}
	if c.HomeyURL != "http://homey" || c.Token != "from-file" || c.Listen != ":9414" || c.Cache() != 3*time.Second || c.Timeout() != 10*time.Second {
		t.Errorf("unexpected config %+v", c)
	}

	t.Setenv("HOMEY_URL", "https://x.connect.athom.com")
	t.Setenv("HOMEY_TOKEN", "env-token")
	t.Setenv("HOMEY_EXPORTER_LISTEN", ":1")
	c, err = LoadConfig(filepath.Join(dir, "does-not-exist.json"))
	if err != nil {
		t.Fatal(err)
	}
	if c.HomeyURL != "https://x.connect.athom.com" || c.Token != "env-token" || c.Listen != ":1" {
		t.Errorf("env override failed: %+v", c)
	}

	t.Setenv("HOMEY_TOKEN", "")
	if _, err := LoadConfig(""); err == nil || !strings.Contains(err.Error(), "token") {
		t.Errorf("want token error, got %v", err)
	}
	t.Setenv("HOMEY_URL", "homey.local")
	t.Setenv("HOMEY_TOKEN", "x")
	if _, err := LoadConfig(""); err == nil || !strings.Contains(err.Error(), "http://") {
		t.Errorf("want scheme error, got %v", err)
	}
	if err := os.WriteFile(cfgFile, []byte(`{bad json`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(cfgFile); err == nil {
		t.Error("want JSON error")
	}
}

func TestClient(t *testing.T) {
	srv, _ := fakeHomey(t)
	c := NewClient(srv.URL, "secret", time.Second)
	devs, err := c.Devices(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	d := devs["dev-1"]
	if d.Name != "Energy Dongle" || d.CapabilitiesObj["measure_power"].Value != 1234.5 || d.CapabilitiesObj["onoff"].Value != true || d.CapabilitiesObj["light_hue"].Value != nil {
		t.Errorf("unexpected device %+v", d)
	}
	sys, err := c.System(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if sys.HomeyModelName != "Homey Pro (2026)" || len(sys.CPUs) != 1 || *sys.VideoCoreUnderVoltNow != true {
		t.Errorf("unexpected system %+v", sys)
	}
	if _, err := NewClient(srv.URL, "wrong", time.Second).Zones(t.Context()); err == nil || !strings.Contains(err.Error(), "HTTP 401") {
		t.Errorf("want 401 error, got %v", err)
	}
}

func TestMetricName(t *testing.T) {
	for _, tc := range []struct{ in, name, sub string }{
		{"measure_power", "homey_device_measure_power", ""},
		{"measure_power.phase2", "homey_device_measure_power", "phase2"},
		{"weird-cap", "homey_device_weird_cap", ""},
		{"a.b.c", "homey_device_a", "b.c"},
	} {
		if n, s := metricName(tc.in); n != tc.name || s != tc.sub {
			t.Errorf("%s: got %s/%s want %s/%s", tc.in, n, s, tc.name, tc.sub)
		}
	}
}

func TestCollector(t *testing.T) {
	srv, hits := fakeHomey(t)
	coll := NewCollector(NewClient(srv.URL, "secret", time.Second), time.Minute, "v-test", "c-test")
	expected := `
# HELP homey_device_available 1 when Homey reports the device as available.
# TYPE homey_device_available gauge
homey_device_available{class="light",device_id="dev-2",device_name="Lamp",zone=""} 0
homey_device_available{class="sensor",device_id="dev-1",device_name="Energy Dongle",zone="Meterkast"} 1
# HELP homey_device_measure_power Homey device capability measure_power.
# TYPE homey_device_measure_power gauge
homey_device_measure_power{class="sensor",device_id="dev-1",device_name="Energy Dongle",sub="",units="W",zone="Meterkast"} 1234.5
homey_device_measure_power{class="sensor",device_id="dev-1",device_name="Energy Dongle",sub="phase2",units="W",zone="Meterkast"} 2500
# HELP homey_device_onoff Homey device capability onoff.
# TYPE homey_device_onoff gauge
homey_device_onoff{class="light",device_id="dev-2",device_name="Lamp",sub="",units="",zone=""} 0
homey_device_onoff{class="sensor",device_id="dev-1",device_name="Energy Dongle",sub="",units="",zone="Meterkast"} 1
# HELP homey_device_weird_cap Homey device capability weird_cap.
# TYPE homey_device_weird_cap gauge
homey_device_weird_cap{class="light",device_id="dev-2",device_name="Lamp",sub="",units="",zone=""} 7
# HELP homey_logic_variable Value of a Homey Logic variable (numbers as-is, booleans 0/1).
# TYPE homey_logic_variable gauge
homey_logic_variable{id="var-1",name="HeatTarget"} 22.5
homey_logic_variable{id="var-2",name="HeatAllowed"} 1
# HELP homey_system_info Homey firmware and model.
# TYPE homey_system_info gauge
homey_system_info{model="Homey Pro (2026)",model_id="homey7q",node_version="v24.22.1",platform="local",version="13.5.3"} 1
# HELP homey_system_load_average System load average.
# TYPE homey_system_load_average gauge
homey_system_load_average{period="15m"} 0.2
homey_system_load_average{period="1m"} 0.3
homey_system_load_average{period="5m"} 0.17
# HELP homey_system_cpu_seconds_total Cumulative CPU time per core and mode.
# TYPE homey_system_cpu_seconds_total counter
homey_system_cpu_seconds_total{cpu="0",mode="idle"} 300
homey_system_cpu_seconds_total{cpu="0",mode="irq"} 0
homey_system_cpu_seconds_total{cpu="0",mode="nice"} 0
homey_system_cpu_seconds_total{cpu="0",mode="sys"} 2
homey_system_cpu_seconds_total{cpu="0",mode="user"} 8
# HELP homey_system_undervoltage 1 when the SoC currently reports under-voltage.
# TYPE homey_system_undervoltage gauge
homey_system_undervoltage 1
# HELP homey_system_memory_component_bytes memory used per component (Homey core, apps, insights, ...).
# TYPE homey_system_memory_component_bytes gauge
homey_system_memory_component_bytes{id="homey",name="Homey"} 500
homey_system_memory_component_bytes{id="homey:app:x",name="X"} 50
# HELP homey_system_memory_swap_bytes Swap in use.
# TYPE homey_system_memory_swap_bytes gauge
homey_system_memory_swap_bytes 100
# HELP homey_system_storage_used_bytes Used storage.
# TYPE homey_system_storage_used_bytes gauge
homey_system_storage_used_bytes 6000
# HELP homey_exporter_up 1 when the last fetch of every Homey endpoint succeeded.
# TYPE homey_exporter_up gauge
homey_exporter_up 1
# HELP homey_exporter_scrape_errors_total Number of failed Homey API calls since start.
# TYPE homey_exporter_scrape_errors_total counter
homey_exporter_scrape_errors_total 0
# HELP homey_exporter_build_info Build information of homey-exporter.
# TYPE homey_exporter_build_info gauge
homey_exporter_build_info{commit="c-test",version="v-test"} 1
`
	if err := testutil.CollectAndCompare(coll, strings.NewReader(expected),
		"homey_device_available", "homey_device_measure_power", "homey_device_onoff", "homey_device_weird_cap",
		"homey_logic_variable", "homey_system_info", "homey_system_load_average", "homey_system_cpu_seconds_total",
		"homey_system_undervoltage", "homey_system_memory_component_bytes", "homey_system_memory_swap_bytes",
		"homey_system_storage_used_bytes", "homey_exporter_up", "homey_exporter_scrape_errors_total", "homey_exporter_build_info"); err != nil {
		t.Error(err)
	}
	// Enum, string and null capabilities are not exported.
	for _, name := range []string{"homey_device_light_mode", "homey_device_hidden", "homey_device_light_hue"} {
		if n := testutil.CollectAndCount(coll, name); n != 0 {
			t.Errorf("%s: want 0 series, got %d", name, n)
		}
	}
	if problems, err := testutil.CollectAndLint(coll); err != nil || len(problems) > 0 {
		t.Errorf("lint: %v %v", err, problems)
	}
	// Second Collect within the TTL reuses the snapshot: still one round of six requests.
	if n := hits.Load(); n != 6 {
		t.Errorf("want 6 requests after cached collects, got %d", n)
	}
}

func TestCollectorPartialFailure(t *testing.T) {
	srv, _ := fakeHomey(t, "/api/manager/devices/device/", "/api/manager/system/memory")
	coll := NewCollector(NewClient(srv.URL, "secret", time.Second), 0, "v", "c")
	expected := `
# HELP homey_exporter_up 1 when the last fetch of every Homey endpoint succeeded.
# TYPE homey_exporter_up gauge
homey_exporter_up 0
# HELP homey_exporter_scrape_errors_total Number of failed Homey API calls since start.
# TYPE homey_exporter_scrape_errors_total counter
homey_exporter_scrape_errors_total 2
# HELP homey_exporter_last_success_timestamp_seconds Unix time of the last fully successful fetch.
# TYPE homey_exporter_last_success_timestamp_seconds gauge
homey_exporter_last_success_timestamp_seconds 0
`
	if err := testutil.CollectAndCompare(coll, strings.NewReader(expected), "homey_exporter_up", "homey_exporter_scrape_errors_total", "homey_exporter_last_success_timestamp_seconds"); err != nil {
		t.Error(err)
	}
	if n := testutil.CollectAndCount(coll, "homey_device_available"); n != 0 {
		t.Errorf("devices failed, want 0 series, got %d", n)
	}
	// Other endpoints still export (TTL 0: this Collect fetches again, errors_total becomes 4).
	if n := testutil.CollectAndCount(coll, "homey_logic_variable", "homey_system_storage_used_bytes"); n != 3 {
		t.Errorf("want 3 series from the healthy endpoints, got %d", n)
	}
}

func TestHandler(t *testing.T) {
	srv, _ := fakeHomey(t)
	h := newHandler(NewCollector(NewClient(srv.URL, "secret", time.Second), time.Minute, "v", "c"))
	for _, tc := range []struct {
		path string
		code int
		body string
	}{
		{"/metrics", 200, "homey_exporter_up 1"},
		{"/healthz", 200, "ok"},
		{"/", 200, "homey-exporter"},
		{"/nope", 404, ""},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if rec.Code != tc.code || !strings.Contains(rec.Body.String(), tc.body) {
			t.Errorf("%s: got %d %q", tc.path, rec.Code, firstLine(rec.Body.String()))
		}
	}
	down := newHandler(NewCollector(NewClient("http://127.0.0.1:1", "secret", 200*time.Millisecond), time.Minute, "v", "c"))
	rec := httptest.NewRecorder()
	down.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("healthz with Homey down: got %d", rec.Code)
	}
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	return s
}
