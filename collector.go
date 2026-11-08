package main

import (
	"context"
	"log"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// snapshot is one fetch of everything the exporter reads from Homey.
type snapshot struct {
	devices  map[string]Device
	zones    map[string]Zone
	vars     map[string]Variable
	system   System
	memory   Usage
	storage  Usage
	ok       [6]bool // devices, zones, vars, system, memory, storage
	at       time.Time
	duration time.Duration
}

func (s *snapshot) allOK() bool {
	for _, ok := range s.ok {
		if !ok {
			return false
		}
	}
	return true
}

// Collector fetches a snapshot from Homey (at most once per TTL) and turns it into metrics.
// It is an unchecked collector: device metric names depend on the capabilities Homey reports.
type Collector struct {
	client *Client
	ttl    time.Duration

	mu   sync.Mutex
	snap *snapshot

	errors      prometheus.Counter
	buildInfo   prometheus.Gauge
	lastSuccess float64
}

// NewCollector returns a collector for client that reuses a snapshot for ttl.
func NewCollector(client *Client, ttl time.Duration, version, commit string) *Collector {
	bi := prometheus.NewGauge(prometheus.GaugeOpts{
		Name:        "homey_exporter_build_info",
		Help:        "Build information of homey-exporter.",
		ConstLabels: prometheus.Labels{"version": version, "commit": commit},
	})
	bi.Set(1)
	return &Collector{
		client: client,
		ttl:    ttl,
		errors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "homey_exporter_scrape_errors_total",
			Help: "Number of failed Homey API calls since start.",
		}),
		buildInfo: bi,
	}
}

// Describe sends nothing: the collector is unchecked (dynamic metric names).
func (c *Collector) Describe(chan<- *prometheus.Desc) {}

// Collect fetches (or reuses) a snapshot and emits all metrics.
func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	s := c.snapshot()
	ch <- c.buildInfo
	ch <- c.errors
	ch <- gauge("homey_exporter_up", "1 when the last fetch of every Homey endpoint succeeded.", b2f(s.allOK()))
	ch <- gauge("homey_exporter_scrape_duration_seconds", "Duration of the last fetch from Homey.", s.duration.Seconds())
	ch <- gauge("homey_exporter_last_success_timestamp_seconds", "Unix time of the last fully successful fetch.", c.lastSuccess)
	if s.ok[0] {
		c.collectDevices(ch, s)
	}
	if s.ok[2] {
		c.collectVariables(ch, s)
	}
	if s.ok[3] {
		c.collectSystem(ch, s)
	}
	if s.ok[4] {
		collectUsage(ch, "memory", s.memory)
	}
	if s.ok[5] {
		collectUsage(ch, "storage", s.storage)
	}
}

// snapshot returns the cached snapshot or fetches a new one when the cache expired.
func (c *Collector) snapshot() *snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.snap != nil && time.Since(c.snap.at) < c.ttl {
		return c.snap
	}
	s := c.fetch()
	if s.allOK() {
		c.lastSuccess = float64(s.at.Unix())
	}
	c.snap = s
	return s
}

// fetch calls the six Homey endpoints concurrently; each failure is logged and counted.
func (c *Collector) fetch() *snapshot {
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), c.client.HTTP.Timeout+time.Second)
	defer cancel()
	s := &snapshot{at: start}
	var wg sync.WaitGroup
	errs := make([]error, 6)
	run := func(i int, f func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = f()
			s.ok[i] = errs[i] == nil
		}()
	}
	run(0, func() (err error) { s.devices, err = c.client.Devices(ctx); return })
	run(1, func() (err error) { s.zones, err = c.client.Zones(ctx); return })
	run(2, func() (err error) { s.vars, err = c.client.Variables(ctx); return })
	run(3, func() (err error) { s.system, err = c.client.System(ctx); return })
	run(4, func() (err error) { s.memory, err = c.client.Memory(ctx); return })
	run(5, func() (err error) { s.storage, err = c.client.Storage(ctx); return })
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			c.errors.Inc()
			log.Printf("homey: %v", err)
		}
	}
	s.duration = time.Since(start)
	return s
}

var unsafeChars = regexp.MustCompile(`[^a-zA-Z0-9_]`)

// metricName maps a capability id to a metric name and a sub-capability label:
// "measure_power.phase2" -> ("homey_device_measure_power", "phase2").
func metricName(capability string) (name, sub string) {
	base, sub, _ := strings.Cut(capability, ".")
	return "homey_device_" + unsafeChars.ReplaceAllString(base, "_"), sub
}

// numeric converts a capability/variable value to a float: numbers as-is, booleans 0/1.
// Strings, enums and null are not exported.
func numeric(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case bool:
		return b2f(x), true
	}
	return 0, false
}

func (c *Collector) collectDevices(ch chan<- prometheus.Metric, s *snapshot) {
	descs := map[string]*prometheus.Desc{}
	seen := map[string]bool{}
	availDesc := prometheus.NewDesc("homey_device_available", "1 when Homey reports the device as available.",
		[]string{"device_id", "device_name", "zone", "class"}, nil)
	for _, id := range sortedKeys(s.devices) {
		d := s.devices[id]
		zone := ""
		if s.ok[1] {
			zone = s.zones[d.Zone].Name
		}
		ch <- prometheus.MustNewConstMetric(availDesc, prometheus.GaugeValue, b2f(d.Available), d.ID, d.Name, zone, d.Class)
		for _, capID := range sortedKeys(d.CapabilitiesObj) {
			cp := d.CapabilitiesObj[capID]
			val, ok := numeric(cp.Value)
			if !ok {
				continue
			}
			name, sub := metricName(capID)
			key := name + "\x00" + d.ID + "\x00" + sub
			if seen[key] {
				continue // two capability ids mapping to the same series
			}
			seen[key] = true
			desc, ok := descs[name]
			if !ok {
				desc = prometheus.NewDesc(name, "Homey device capability "+strings.TrimPrefix(name, "homey_device_")+".",
					[]string{"device_id", "device_name", "zone", "class", "sub", "units"}, nil)
				descs[name] = desc
			}
			ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, val, d.ID, d.Name, zone, d.Class, sub, cp.Units)
		}
	}
}

func (c *Collector) collectVariables(ch chan<- prometheus.Metric, s *snapshot) {
	desc := prometheus.NewDesc("homey_logic_variable", "Value of a Homey Logic variable (numbers as-is, booleans 0/1).",
		[]string{"id", "name"}, nil)
	for _, id := range sortedKeys(s.vars) {
		v := s.vars[id]
		if val, ok := numeric(v.Value); ok {
			ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, val, v.ID, v.Name)
		}
	}
}

func (c *Collector) collectSystem(ch chan<- prometheus.Metric, s *snapshot) {
	sys := s.system
	ch <- prometheus.MustNewConstMetric(prometheus.NewDesc("homey_system_info", "Homey firmware and model.",
		[]string{"version", "model", "model_id", "platform", "node_version"}, nil),
		prometheus.GaugeValue, 1, sys.HomeyVersion, sys.HomeyModelName, sys.HomeyModelID, sys.HomeyPlatform, sys.NodeVersion)
	load := prometheus.NewDesc("homey_system_load_average", "System load average.", []string{"period"}, nil)
	for i, p := range []string{"1m", "5m", "15m"} {
		if i < len(sys.LoadAvg) {
			ch <- prometheus.MustNewConstMetric(load, prometheus.GaugeValue, sys.LoadAvg[i], p)
		}
	}
	ch <- gauge("homey_system_uptime_seconds", "Homey uptime.", sys.Uptime)
	cpu := prometheus.NewDesc("homey_system_cpu_seconds_total", "Cumulative CPU time per core and mode.", []string{"cpu", "mode"}, nil)
	for i, core := range sys.CPUs {
		for _, mode := range sortedKeys(core.Times) {
			ch <- prometheus.MustNewConstMetric(cpu, prometheus.CounterValue, core.Times[mode]/1000, strconv.Itoa(i), mode)
		}
	}
	if sys.VideoCoreTemperature != nil {
		ch <- gauge("homey_system_temperature_celsius", "SoC temperature.", *sys.VideoCoreTemperature)
	}
	if sys.VideoCoreThrottleNow != nil {
		ch <- gauge("homey_system_throttled", "1 when the SoC is currently throttled.", b2f(*sys.VideoCoreThrottleNow))
	}
	if sys.VideoCoreUnderVoltNow != nil {
		ch <- gauge("homey_system_undervoltage", "1 when the SoC currently reports under-voltage.", b2f(*sys.VideoCoreUnderVoltNow))
	}
}

// collectUsage emits totals and the per-component breakdown of memory or storage.
func collectUsage(ch chan<- prometheus.Metric, kind string, u Usage) {
	ch <- gauge("homey_system_"+kind+"_total_bytes", "Total "+kind+".", u.Total)
	ch <- gauge("homey_system_"+kind+"_free_bytes", "Free "+kind+".", u.Free)
	if kind == "storage" {
		ch <- gauge("homey_system_storage_used_bytes", "Used storage.", u.Used)
	} else {
		ch <- gauge("homey_system_memory_swap_bytes", "Swap in use.", u.Swap)
	}
	desc := prometheus.NewDesc("homey_system_"+kind+"_component_bytes", kind+" used per component (Homey core, apps, insights, ...).",
		[]string{"id", "name"}, nil)
	for _, id := range sortedKeys(u.Types) {
		ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, u.Types[id].Size, id, u.Types[id].Name)
	}
}

func gauge(name, help string, v float64) prometheus.Metric {
	return prometheus.MustNewConstMetric(prometheus.NewDesc(name, help, nil, nil), prometheus.GaugeValue, v)
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func sortedKeys[V any](m map[string]V) []string { return slices.Sorted(maps.Keys(m)) }
