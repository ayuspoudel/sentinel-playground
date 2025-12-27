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
			Help: "Total number of HTTP requests",
		},
		[]string{"status", "app", "deployment", "cluster"},
	)
)

func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("missing required env var: %s", key)
	}
	return v
}

func main() {
	rand.Seed(time.Now().UnixNano())

	appName := mustEnv("APP_NAME")
	deployment := mustEnv("DEPLOYMENT_NAME")
	cluster := mustEnv("CLUSTER_NAME")

	errorRate := 0.0
	if v := os.Getenv("ERROR_RATE"); v != "" {
		r, err := strconv.ParseFloat(v, 64)
		if err != nil || r < 0 || r > 1 {
			log.Fatalf("invalid ERROR_RATE: %s", v)
		}
		errorRate = r
	}

	prometheus.MustRegister(httpRequestsTotal)

	mux := http.NewServeMux()

	mux.HandleFunc("/work", func(w http.ResponseWriter, _ *http.Request) {
		if rand.Float64() < errorRate {
			httpRequestsTotal.WithLabelValues("500", appName, deployment, cluster).Inc()
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("error"))
			return
		}

		httpRequestsTotal.WithLabelValues("200", appName, deployment, cluster).Inc()
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	mux.Handle("/metrics", promhttp.Handler())

	addr := ":8080"
	if p := os.Getenv("PORT"); p != "" {
		addr = ":" + p
	}

	log.Printf(
		"workload started app=%s deployment=%s cluster=%s errorRate=%.2f",
		appName, deployment, cluster, errorRate,
	)

	server := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	log.Fatal(server.ListenAndServe())
}
