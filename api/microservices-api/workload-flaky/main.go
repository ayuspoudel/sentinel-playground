package main

import (
	"log"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	httpRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests ",
		},
		[]string{"status", "app", "deployment", "cluster"},
	)
)

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("missing env var: %s", key)
	}
	return v
}

func main() {
	rand.Seed(time.Now().UnixNano())

	app := mustEnv("APP_NAME")
	deployment := mustEnv("DEPLOYMENT_NAME")
	cluster := mustEnv("CLUSTER_NAME")

	// baseline error rate
	baseError := 0.01
	if v := os.Getenv("ERROR_RATE"); v != "" {
		baseError, _ = strconv.ParseFloat(v, 64)
	}

	// spike probability (how often a bad window starts)
	spikeChance := 0.05 // 5%
	if v := os.Getenv("SPIKE_PROBABILITY"); v != "" {
		spikeChance, _ = strconv.ParseFloat(v, 64)
	}

	// spike duration
	spikeDuration := 10 * time.Second
	if v := os.Getenv("SPIKE_DURATION_SEC"); v != "" {
		d, _ := strconv.Atoi(v)
		spikeDuration = time.Duration(d) * time.Second
	}

	// spike error rate
	spikeError := 0.3
	if v := os.Getenv("SPIKE_ERROR_RATE"); v != "" {
		spikeError, _ = strconv.ParseFloat(v, 64)
	}

	// latency jitter
	maxLatencyMs := 200
	if v := os.Getenv("MAX_LATENCY_MS"); v != "" {
		maxLatencyMs, _ = strconv.Atoi(v)
	}

	prometheus.MustRegister(httpRequestsTotal)

	var spikeUntil time.Time

	mux := http.NewServeMux()

	mux.HandleFunc("/work", func(w http.ResponseWriter, _ *http.Request) {
		now := time.Now()

		// randomly enter spike window
		if now.After(spikeUntil) && rand.Float64() < spikeChance {
			spikeUntil = now.Add(spikeDuration)
			log.Println("entering flaky spike window")
		}

		// latency jitter
		if maxLatencyMs > 0 {
			time.Sleep(time.Duration(rand.Intn(maxLatencyMs)) * time.Millisecond)
		}

		errRate := baseError
		if now.Before(spikeUntil) {
			errRate = spikeError
		}

		if rand.Float64() < errRate {
			httpRequestsTotal.WithLabelValues("500", app, deployment, cluster).Inc()
			w.WriteHeader(500)
			w.Write([]byte("error"))
			return
		}

		httpRequestsTotal.WithLabelValues("200", app, deployment, cluster).Inc()
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	})

	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	})

	mux.Handle("/metrics", promhttp.Handler())

	log.Println("flaky workload started")
	log.Fatal(http.ListenAndServe(":8080", mux))
}
