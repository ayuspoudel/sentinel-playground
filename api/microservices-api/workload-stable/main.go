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

	errorRate := 0.0
	if v := os.Getenv("ERROR_RATE"); v != "" {
		r, _ := strconv.ParseFloat(v, 64)
		errorRate = r
	}

	prometheus.MustRegister(httpRequestsTotal)

	mux := http.NewServeMux()

	mux.HandleFunc("/work", func(w http.ResponseWriter, _ *http.Request) {
		if rand.Float64() < errorRate {
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

	log.Println("stable workload started")
	log.Fatal(http.ListenAndServe(":8080", mux))
}
