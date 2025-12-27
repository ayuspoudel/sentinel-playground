package main

import (
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var sentRequests uint64

var (
	loadgenRequestsTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: "loadgen",
			Name:      "requests_total",
			Help:      "Total number of requests attempted by load generator",
		},
	)

	loadgenErrorsTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Namespace: "loadgen",
			Name:      "errors_total",
			Help:      "Total number of client-side errors",
		},
	)

	loadgenInflight = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "loadgen",
			Name:      "inflight_requests",
			Help:      "Current number of in-flight requests",
		},
	)

	loadgenRequestDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Namespace: "loadgen",
			Name:      "request_duration_seconds",
			Help:      "End-to-end request latency from load generator",
			Buckets:   []float64{0.05, 0.1, 0.2, 0.3, 0.5, 0.75, 1, 1.5, 2, 3},
		},
	)

	loadgenTargetUp = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "loadgen",
			Name:      "target_up",
			Help:      "Whether the target endpoint is reachable",
		},
	)
)

func main() {
	targetURL := mustGetEnv("TARGET_URL")
	concurrency := getEnvInt("CONCURRENCY", 10)
	sleepMs := getEnvInt("SLEEP_MS", 50)

	/*
		@ayuspoudel
		http.Transport controls how aggresively conections are reused so load generation
		stresses the service not TCP connection. Under the hood, it opens 200 open connections
		and keep them idle. This has to be always greater than concurrency, if not connections will
		be constantly opened and closed, making the client bottleneck instead of service.
	*/
	maxIdleConns := 1000
	maxIdleConnsPerHost := 1000

	if concurrency >= maxIdleConns {
		maxIdleConns = concurrency + 50
	}
	if concurrency >= maxIdleConnsPerHost {
		maxIdleConnsPerHost = concurrency + 50
	}

	transport := &http.Transport{
		MaxIdleConns:        maxIdleConns,
		MaxIdleConnsPerHost: maxIdleConnsPerHost,
		IdleConnTimeout:     90 * time.Second,
		DisableKeepAlives:   false,
		ForceAttemptHTTP2:   false,
	}
	/*
		@ayuspoudel
		http client helps add policies and rules for any http request. We will be using
		http.Get and using this client which has timeOut of 2s and a transport policy
		we can establish http connections and do get requests on the target url.
	*/
	client := &http.Client{
		Transport: transport,
		Timeout:   2 * time.Second,
	}

	// register metrics
	prometheus.MustRegister(
		loadgenRequestsTotal,
		loadgenErrorsTotal,
		loadgenInflight,
		loadgenRequestDuration,
		loadgenTargetUp,
	)

	// metrics endpoint
	go func() {
		http.Handle("/metrics", promhttp.Handler())
		log.Println("loadgen metrics listening on :9090")
		log.Fatal(http.ListenAndServe(":9090", nil))
	}()

	log.Println("load generator started")
	log.Println("target:", targetURL)
	log.Println("concurrency:", concurrency)
	log.Println("sleep(ms):", sleepMs)

	// stats logger
	go logStats()

	var wg sync.WaitGroup
	wg.Add(concurrency)

	for i := 0; i < concurrency; i++ {
		go worker(i, client, targetURL, sleepMs, &wg)
	}

	wg.Wait()
}

/*
@ayuspoudel
Each worker thread will need to  send one HTTP GET  request. It needs to then
wait for response or error. It needs to drain and close the response body,
and also atomically increment the shared counter. It needs to sleep for a
fixed amount of time and repeat forever.
*/
func worker(id int, client *http.Client, target string, sleepMs int, wg *sync.WaitGroup) {
	defer wg.Done()

	for {
		start := time.Now()
		loadgenInflight.Inc()

		resp, err := client.Get(target)
		if err != nil {
			loadgenErrorsTotal.Inc()
			loadgenTargetUp.Set(0)
			loadgenInflight.Dec()

			log.Printf("worker %d error: %v", id, err)
			time.Sleep(200 * time.Millisecond)
			continue
		}

		loadgenTargetUp.Set(1)

		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()

		loadgenRequestsTotal.Inc()
		loadgenRequestDuration.Observe(time.Since(start).Seconds())
		loadgenInflight.Dec()

		atomic.AddUint64(&sentRequests, 1)
		/*
			This ensures total [ load = concurrency * (1/(requestTime + sleep) ])
		*/
		time.Sleep(time.Duration(sleepMs) * time.Millisecond)
	}
}

/*
Ticks a timer of 5s, for everytime
*/
func logStats() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		count := atomic.SwapUint64(&sentRequests, 0)
		rps := float64(count) / 5.0

		log.Printf(
			"loadgen stats: requests_last_5s=%d rps=%.2f",
			count,
			rps,
		)
	}
}

func mustGetEnv(key string) string {
	val := os.Getenv(key)
	if val == "" {
		log.Fatalf("%s environment variable is required", key)
	}
	return val
}

func getEnvInt(key string, defaultVal int) int {
	val := os.Getenv(key)
	if val == "" {
		return defaultVal
	}

	parsed, err := strconv.Atoi(val)
	if err != nil {
		log.Fatalf("invalid value for %s: %s", key, val)
	}

	return parsed
}
