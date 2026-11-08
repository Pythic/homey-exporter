// Command homey-exporter exposes Homey Pro devices, Logic variables and system health
// as Prometheus metrics by polling the Homey Web API.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Set at build time: -ldflags "-X main.version=v0.1.0 -X main.commit=abc1234".
var (
	version = "dev"
	commit  = "none"
)

func main() {
	cfgPath := flag.String("config", "homey-exporter.json", "path to the JSON config file (optional when HOMEY_URL and HOMEY_TOKEN are set)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Printf("homey-exporter %s (%s)\n", version, commit)
		return
	}
	cfg, err := LoadConfig(*cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	coll := NewCollector(NewClient(cfg.HomeyURL, cfg.Token, cfg.Timeout()), cfg.Cache(), version, commit)
	srv := &http.Server{Addr: cfg.Listen, Handler: newHandler(coll), ReadHeaderTimeout: 10 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	log.Printf("homey-exporter %s listening on %s, homey %s, cache %s", version, cfg.Listen, cfg.HomeyURL, cfg.Cache())
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

// newHandler serves /metrics, /healthz and a landing page.
func newHandler(coll *Collector) http.Handler {
	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}), coll)
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{ErrorHandling: promhttp.ContinueOnError}))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		if coll.snapshot().allOK() {
			_, _ = fmt.Fprintln(w, "ok")
			return
		}
		http.Error(w, "homey unreachable", http.StatusServiceUnavailable)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		_, _ = fmt.Fprint(w, `<html><head><title>homey-exporter</title></head><body><h1>homey-exporter</h1><p><a href="/metrics">metrics</a> · <a href="/healthz">healthz</a></p></body></html>`)
	})
	return mux
}
