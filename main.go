package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
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

func main() {
	// Logger
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

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

	// S3 Select metrics
	s3Str := getEnv("ENABLE_S3SELECT_METRICS", "false")
	enableS3SelectMetrics, err := strconv.ParseBool(s3Str)
	if err != nil {
		slog.Warn("Invalid ENABLE_S3SELECT_METRICS value, using false",
			"value", s3Str,
			"error", err,
		)
		enableS3SelectMetrics = false
	}

	collector := NewRADOSGWCollector(
		endpoint,
		accessKey,
		secretKey,
		insecure,
		scrapeTimeout,
		enableUserStats,
		enableBucketStats,
		enableUsageMetrics,
		enableS3SelectMetrics,
		logger,
	)

	prometheus.MustRegister(collector)

	server := &http.Server{
		Addr:    ":" + port,
		Handler: promhttp.Handler(),
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
