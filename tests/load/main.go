// Command load runs a small, dependency-free HTTP load test for local capacity
// checks. It deliberately lives outside the API binary so production code does
// not carry a load-generation dependency.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type config struct {
	url           string
	duration      time.Duration
	concurrency   int
	timeout       time.Duration
	minRPS        float64
	maxP95        time.Duration
	maxErrorRate  float64
	requestMethod string
}

type workerResult struct {
	durations []time.Duration
	successes int
	failures  int
}

type report struct {
	total        int
	successes    int
	failures     int
	durations    []time.Duration
	elapsed      time.Duration
	p50          time.Duration
	p95          time.Duration
	p99          time.Duration
	max          time.Duration
	errorRate    float64
	requestsPerS float64
}

func main() {
	cfg := loadConfig()
	result, err := run(context.Background(), cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load test failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf(
		"load test result: url=%s method=%s total=%d success=%d failure=%d error_rate=%.4f rps=%.2f p50=%s p95=%s p99=%s max=%s\n",
		cfg.url, cfg.requestMethod, result.total, result.successes, result.failures,
		result.errorRate, result.requestsPerS, result.p50, result.p95, result.p99, result.max,
	)
	if cfg.minRPS > 0 && result.requestsPerS < cfg.minRPS {
		fmt.Fprintf(os.Stderr, "minimum RPS not met: got %.2f, want >= %.2f\n", result.requestsPerS, cfg.minRPS)
		os.Exit(1)
	}
	if cfg.maxP95 > 0 && result.p95 > cfg.maxP95 {
		fmt.Fprintf(os.Stderr, "P95 latency too high: got %s, want <= %s\n", result.p95, cfg.maxP95)
		os.Exit(1)
	}
	if result.errorRate > cfg.maxErrorRate {
		fmt.Fprintf(os.Stderr, "error rate too high: got %.4f, want <= %.4f\n", result.errorRate, cfg.maxErrorRate)
		os.Exit(1)
	}
	fmt.Println("load test passed")
}

func loadConfig() config {
	defaultDuration := envDuration("LOAD_TEST_DURATION", 10*time.Second)
	defaultConcurrency := envInt("LOAD_TEST_CONCURRENCY", 20)
	defaultTimeout := envDuration("LOAD_TEST_TIMEOUT", 2*time.Second)
	defaultMaxErrorRate := envFloat("LOAD_TEST_MAX_ERROR_RATE", 0.01)

	var cfg config
	flag.StringVar(&cfg.url, "url", envString("LOAD_TEST_URL", "http://127.0.0.1:8080/products"), "HTTP URL to exercise")
	flag.DurationVar(&cfg.duration, "duration", defaultDuration, "measurement duration")
	flag.IntVar(&cfg.concurrency, "concurrency", defaultConcurrency, "number of concurrent workers")
	flag.DurationVar(&cfg.timeout, "timeout", defaultTimeout, "per-request timeout")
	flag.Float64Var(&cfg.minRPS, "min-rps", envFloat("LOAD_TEST_MIN_RPS", 0), "minimum accepted requests per second")
	flag.DurationVar(&cfg.maxP95, "max-p95", envDuration("LOAD_TEST_MAX_P95", 0), "maximum accepted P95 latency")
	flag.Float64Var(&cfg.maxErrorRate, "max-error-rate", defaultMaxErrorRate, "maximum accepted error ratio")
	flag.StringVar(&cfg.requestMethod, "method", envString("LOAD_TEST_METHOD", http.MethodGet), "HTTP method")
	flag.Parse()
	return cfg
}

func run(parent context.Context, cfg config) (report, error) {
	if strings.TrimSpace(cfg.url) == "" {
		return report{}, fmt.Errorf("url is required")
	}
	if cfg.duration <= 0 || cfg.concurrency <= 0 || cfg.timeout <= 0 {
		return report{}, fmt.Errorf("duration, concurrency and timeout must be positive")
	}
	if cfg.maxErrorRate < 0 || cfg.maxErrorRate > 1 {
		return report{}, fmt.Errorf("max-error-rate must be between 0 and 1")
	}
	if _, err := http.NewRequestWithContext(parent, cfg.requestMethod, cfg.url, nil); err != nil {
		return report{}, fmt.Errorf("build request: %w", err)
	}

	client := &http.Client{Transport: &http.Transport{
		MaxIdleConns:        cfg.concurrency * 2,
		MaxIdleConnsPerHost: cfg.concurrency,
		IdleConnTimeout:     30 * time.Second,
	}}
	defer client.CloseIdleConnections()

	ctx, cancel := context.WithTimeout(parent, cfg.duration)
	defer cancel()
	startedAt := time.Now()
	results := make(chan workerResult, cfg.concurrency)
	var workers sync.WaitGroup
	workers.Add(cfg.concurrency)
	for range cfg.concurrency {
		go func() {
			defer workers.Done()
			results <- runWorker(ctx, client, cfg)
		}()
	}
	workers.Wait()
	close(results)

	result := report{elapsed: time.Since(startedAt)}
	for worker := range results {
		result.successes += worker.successes
		result.failures += worker.failures
		result.total += len(worker.durations)
		// 每个 worker 的切片只在这里被消费，合并后统一排序计算分位数。
		result.durations = append(result.durations, worker.durations...)
	}
	if result.total == 0 {
		return report{}, fmt.Errorf("no requests completed")
	}
	if result.elapsed <= 0 {
		result.elapsed = time.Nanosecond
	}
	result.errorRate = float64(result.failures) / float64(result.total)
	result.requestsPerS = float64(result.total) / result.elapsed.Seconds()
	sort.Slice(result.durations, func(i, j int) bool { return result.durations[i] < result.durations[j] })
	result.p50 = percentile(result.durations, 0.50)
	result.p95 = percentile(result.durations, 0.95)
	result.p99 = percentile(result.durations, 0.99)
	result.max = result.durations[len(result.durations)-1]
	return result, nil
}

func runWorker(ctx context.Context, client *http.Client, cfg config) workerResult {
	result := workerResult{}
	for {
		select {
		case <-ctx.Done():
			return result
		default:
		}

		requestCtx, cancel := context.WithTimeout(context.Background(), cfg.timeout)
		startedAt := time.Now()
		request, err := http.NewRequestWithContext(requestCtx, cfg.requestMethod, cfg.url, nil)
		if err != nil {
			result.durations = append(result.durations, time.Since(startedAt))
			cancel()
			result.failures++
			continue
		}
		response, err := client.Do(request)
		latency := time.Since(startedAt)
		result.durations = append(result.durations, latency)
		if err != nil {
			result.failures++
			cancel()
			continue
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		cancel()
		if response.StatusCode == http.StatusOK {
			result.successes++
		} else {
			result.failures++
		}
	}
}

func percentile(values []time.Duration, fraction float64) time.Duration {
	if len(values) == 0 {
		return 0
	}
	index := int(math.Ceil(float64(len(values))*fraction)) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(values) {
		index = len(values) - 1
	}
	return values[index]
}

func envString(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envDuration(name string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envInt(name string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envFloat(name string, fallback float64) float64 {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fallback
	}
	return parsed
}
