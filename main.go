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
	// Configure logger
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	// Load configuration from environment
	endpoint := getEnv("RADOSGW_ENDPOINT", "")
	accessKey := getEnv("ACCESS_KEY", "")
	secretKey := getEnv("SECRET_KEY", "")
	if endpoint == "" || accessKey == "" || secretKey == "" {
		slog.Error("Required environment variables: RADOSGW_ENDPOINT, ACCESS_KEY, SECRET_KEY")
		os.Exit(1)
	}

	store := getEnv("STORE", "us-east-1")
	port := getEnv("METRICS_PORT", "9242")
	insecure, _ := strconv.ParseBool(getEnv("INSECURE_SKIP_VERIFY", "false"))

	// Create collector with logger
	collector := NewRADOSGWCollector(endpoint, accessKey, secretKey, store, insecure, logger)
	prometheus.MustRegister(collector)

	// HTTP server
	server := &http.Server{
		Addr:    ":" + port,
		Handler: promhttp.Handler(),
	}

	// Start server in background
	go func() {
		slog.Info("RADOSGW exporter started", "port", port, "endpoint", endpoint)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("HTTP server failed", "error", err)
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	slog.Info("Shutdown signal received, initiating graceful shutdown...")

	// Graceful shutdown with 10s timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		slog.Error("Graceful shutdown failed", "error", err)
		os.Exit(1)
	}

	slog.Info("Server stopped")
}
