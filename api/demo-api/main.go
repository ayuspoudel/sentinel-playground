package main

import (
	"log"
	"math/rand"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	httpRequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "demoapp",
			Name:      "http_requests_total",
			Help:      "Total number of HTTP requests",
			Subsystem: "http",
		}, []string{"method", "path", "status_class"},
	)
	httpRequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "realapp",
			Subsystem: "http",
			Name:      "request_duration_seconds",
			Help:      "HTTP request latency",
			Buckets:   []float64{0.05, 0.1, 0.2, 0.3, 0.5, 0.75, 1, 1.5, 2, 3},
		},
		[]string{"method", "path"},
	)
	inflightRequests = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "realapp",
			Subsystem: "http",
			Name:      "inflight_requests",
			Help:      "Current number of in-flight HTTP requests",
		},
	)
)

func main() {
	rand.Seed(time.Now().UnixNano())

	// Register metrics with the default registry
	prometheus.MustRegister(
		httpRequestsTotal,
		httpRequestDuration,
		inflightRequests,
	)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		inflightRequests.Inc()
		defer inflightRequests.Dec()

		// Simulate work
		delay := time.Duration(50+rand.Intn(250)) * time.Millisecond
		time.Sleep(delay)

		statusCode := http.StatusOK
		if rand.Intn(10) == 0 {
			statusCode = http.StatusInternalServerError
		}

		w.WriteHeader(statusCode)
		w.Write([]byte("hello from realapp \n"))

		statusClass := strconv.Itoa(statusCode/100) + "xx"

		httpRequestsTotal.WithLabelValues(
			r.Method,
			r.URL.Path,
			statusClass,
		).Inc()

		httpRequestDuration.WithLabelValues(
			r.Method,
			r.URL.Path,
		).Observe(time.Since(start).Seconds())
	})

	http.Handle("/", handler)
	http.Handle("/metrics", promhttp.Handler())

	log.Println("realapp listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
