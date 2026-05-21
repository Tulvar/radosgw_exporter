package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func getEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func parseDurationEnv(raw string) (time.Duration, error) {
	if raw == "" {
		return 0, nil
	}
	if seconds, err := strconv.Atoi(raw); err == nil {
		return time.Duration(seconds) * time.Second, nil
	}
	return time.ParseDuration(raw)
}

func main() {
	// Logger config
	logLevelStr := strings.ToLower(getEnv("LOG_LEVEL", "info"))
	logFormatStr := strings.ToLower(getEnv("LOG_FORMAT", "json"))

	var logLevel slog.Level
	levelValid := true
	switch logLevelStr {
	case "debug":
		logLevel = slog.LevelDebug
	case "info":
		logLevel = slog.LevelInfo
	case "warn", "warning":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		levelValid = false
		logLevel = slog.LevelInfo
	}

	formatValid := true
	var handler slog.Handler
	switch logFormatStr {
	case "json":
		handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel})
	case "text":
		handler = slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel})
	default:
		formatValid = false
		handler = slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel})
	}

	logger := slog.New(handler)
	slog.SetDefault(logger)

	if !levelValid {
		slog.Warn("Invalid LOG_LEVEL value, using info", "value", logLevelStr)
	}
	if !formatValid {
		slog.Warn("Invalid LOG_FORMAT value, using json", "value", logFormatStr)
	}

	// Required config
	endpoint := getEnv("RADOSGW_ENDPOINT", "")
	accessKey := getEnv("ACCESS_KEY", "")
	secretKey := getEnv("SECRET_KEY", "")
	if endpoint == "" || accessKey == "" || secretKey == "" {
		slog.Error("Required env vars: RADOSGW_ENDPOINT, ACCESS_KEY, SECRET_KEY")
		os.Exit(1)
	}

	port := getEnv("METRICS_PORT", "9242")

	// TLS verify
	insecureStr := getEnv("INSECURE_SKIP_VERIFY", "false")
	insecure, err := strconv.ParseBool(insecureStr)
	if err != nil {
		slog.Warn("Invalid INSECURE_SKIP_VERIFY value, using false",
			"value", insecureStr,
			"error", err,
		)
		insecure = false
	}

	// Scrape timeout
	scrapeTimeoutStr := getEnv("SCRAPE_TIMEOUT", "15")
	scrapeTimeoutSec, err := strconv.Atoi(scrapeTimeoutStr)
	if err != nil || scrapeTimeoutSec <= 0 {
		slog.Warn("Invalid SCRAPE_TIMEOUT value, using 15s",
			"value", scrapeTimeoutStr,
			"error", err,
		)
		scrapeTimeoutSec = 15
	}
	scrapeTimeout := time.Duration(scrapeTimeoutSec) * time.Second

	usageCacheTTLStr := getEnv("USAGE_CACHE_TTL", "0s")
	usageCacheTTL, err := parseDurationEnv(usageCacheTTLStr)
	if err != nil || usageCacheTTL < 0 {
		slog.Warn("Invalid USAGE_CACHE_TTL value, using 0s (disabled)",
			"value", usageCacheTTLStr,
			"error", err,
		)
		usageCacheTTL = 0
	}

	usersBucketsCacheTTLStr := getEnv("USERS_BUCKETS_CACHE_TTL", "0s")
	usersBucketsCacheTTL, err := parseDurationEnv(usersBucketsCacheTTLStr)
	if err != nil || usersBucketsCacheTTL < 0 {
		slog.Warn("Invalid USERS_BUCKETS_CACHE_TTL value, using 0s (disabled)",
			"value", usersBucketsCacheTTLStr,
			"error", err,
		)
		usersBucketsCacheTTL = 0
	}

	// User metrics
	userStr := getEnv("ENABLE_USER_STATS", "true")
	enableUserStats, err := strconv.ParseBool(userStr)
	if err != nil {
		slog.Warn("Invalid ENABLE_USER_STATS value, using true",
			"value", userStr,
			"error", err,
		)
		enableUserStats = true
	}

	// Bucket metrics
	bucketStr := getEnv("ENABLE_BUCKET_STATS", "true")
	enableBucketStats, err := strconv.ParseBool(bucketStr)
	if err != nil {
		slog.Warn("Invalid ENABLE_BUCKET_STATS value, using true",
			"value", bucketStr,
			"error", err,
		)
		enableBucketStats = true
	}

	// Usage metrics
	usageStr := getEnv("ENABLE_USAGE_METRICS", "false")
	enableUsageMetrics, err := strconv.ParseBool(usageStr)
	if err != nil {
		slog.Warn("Invalid ENABLE_USAGE_METRICS value, using false",
			"value", usageStr,
			"error", err,
		)
		enableUsageMetrics = false
	}

	maxUsersPerScrapeStr := getEnv("MAX_USERS_PER_SCRAPE", "0")
	maxUsersPerScrape, err := strconv.Atoi(maxUsersPerScrapeStr)
	if err != nil || maxUsersPerScrape < 0 {
		slog.Warn("Invalid MAX_USERS_PER_SCRAPE value, using 0 (all users per scrape)",
			"value", maxUsersPerScrapeStr,
			"error", err,
		)
		maxUsersPerScrape = 0
	}

	collector, err := NewRADOSGWCollector(
		endpoint,
		accessKey,
		secretKey,
		insecure,
		scrapeTimeout,
		usageCacheTTL,
		usersBucketsCacheTTL,
		enableUserStats,
		enableBucketStats,
		enableUsageMetrics,
		maxUsersPerScrape,
		logger,
	)
	if err != nil {
		slog.Error("Failed to create RADOSGW collector", "error", err)
		os.Exit(1)
	}

	prometheus.MustRegister(collector)

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	server := &http.Server{
		Addr:    ":" + port,
		Handler: mux,
	}

	go func() {
		slog.Info("RADOSGW exporter started", "port", port, "endpoint", endpoint)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("HTTP server failed", "error", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	slog.Info("Shutdown signal received, graceful shutdown...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		slog.Error("Graceful shutdown failed", "error", err)
		os.Exit(1)
	}

	slog.Info("Server stopped")
}
