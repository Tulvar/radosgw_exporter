package main

import (
	"context"
	"crypto/tls"
	"log/slog"
	"net/http"
	"time"

	"github.com/ceph/go-ceph/rgw/admin"
	"github.com/prometheus/client_golang/prometheus"
)

// usageMetricKey — unique key for usage metric aggregation
type usageMetricKey struct {
	bucket, owner, category, store string
}

// usageMetricValues — aggregated metric values
type usageMetricValues struct {
	ops, successfulOps, bytesSent, bytesReceived float64
}

// RADOSGWCollector implements prometheus.Collector
type RADOSGWCollector struct {
	client *admin.API
	store  string
	logger *slog.Logger

	// Usage metrics
	ops           *prometheus.Desc
	successfulOps *prometheus.Desc
	bytesSent     *prometheus.Desc
	bytesReceived *prometheus.Desc

	// Bucket metrics
	bucketUsageBytes   *prometheus.Desc
	bucketUsageObjects *prometheus.Desc

	// User metrics
	userTotalBytes   *prometheus.Desc
	userTotalObjects *prometheus.Desc

	// User quotas
	userQuotaEnabled      *prometheus.Desc
	userQuotaMaxSizeBytes *prometheus.Desc
	userQuotaMaxObjects   *prometheus.Desc

	// Per-user bucket quotas
	userBucketQuotaEnabled      *prometheus.Desc
	userBucketQuotaMaxSizeBytes *prometheus.Desc
	userBucketQuotaMaxObjects   *prometheus.Desc

	// System metrics
	scrapeDurationSeconds *prometheus.Desc
	up                    *prometheus.Desc
}

// NewRADOSGWCollector creates a new collector
func NewRADOSGWCollector(endpoint, accessKey, secretKey, store string, insecure bool, logger *slog.Logger) *RADOSGWCollector {
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: insecure,
			},
		},
	}

	client, err := admin.New(endpoint, accessKey, secretKey, httpClient)
	if err != nil {
		logger.Error("Failed to create RGW admin client", "error", err)
		panic(err)
	}

	bucketLabels := []string{"bucket", "owner", "category", "store"}
	userLabels := []string{"user", "store"}

	return &RADOSGWCollector{
		client: client,
		store:  store,
		logger: logger,

		// Usage
		ops: prometheus.NewDesc(
			"radosgw_usage_ops_total",
			"Number of operations",
			bucketLabels, nil,
		),
		successfulOps: prometheus.NewDesc(
			"radosgw_usage_successful_ops_total",
			"Number of successful operations",
			bucketLabels, nil,
		),
		bytesSent: prometheus.NewDesc(
			"radosgw_usage_sent_bytes_total",
			"Bytes sent by the RADOSGW",
			bucketLabels, nil,
		),
		bytesReceived: prometheus.NewDesc(
			"radosgw_usage_received_bytes_total",
			"Bytes received by the RADOSGW",
			bucketLabels, nil,
		),

		// Bucket
		bucketUsageBytes: prometheus.NewDesc(
			"radosgw_usage_bucket_bytes",
			"Bucket used bytes",
			bucketLabels, nil,
		),
		bucketUsageObjects: prometheus.NewDesc(
			"radosgw_usage_bucket_objects",
			"Number of objects in bucket",
			bucketLabels, nil,
		),

		// User
		userTotalBytes: prometheus.NewDesc(
			"radosgw_usage_user_total_bytes",
			"Usage of bytes by user",
			userLabels, nil,
		),
		userTotalObjects: prometheus.NewDesc(
			"radosgw_usage_user_total_objects",
			"Usage of objects by user",
			userLabels, nil,
		),

		// User Quota
		userQuotaEnabled: prometheus.NewDesc(
			"radosgw_usage_user_quota_enabled",
			"User quota enabled",
			userLabels, nil,
		),
		userQuotaMaxSizeBytes: prometheus.NewDesc(
			"radosgw_usage_user_quota_size_bytes",
			"Maximum allowed size in bytes for user",
			userLabels, nil,
		),
		userQuotaMaxObjects: prometheus.NewDesc(
			"radosgw_usage_user_quota_size_objects",
			"Maximum allowed number of objects across all user buckets",
			userLabels, nil,
		),

		// Bucket Quota (per-user)
		userBucketQuotaEnabled: prometheus.NewDesc(
			"radosgw_usage_user_bucket_quota_enabled",
			"User per-bucket-quota enabled",
			userLabels, nil,
		),
		userBucketQuotaMaxSizeBytes: prometheus.NewDesc(
			"radosgw_usage_user_bucket_quota_size_bytes",
			"Maximum allowed size in bytes for each bucket of user",
			userLabels, nil,
		),
		userBucketQuotaMaxObjects: prometheus.NewDesc(
			"radosgw_usage_user_bucket_quota_size_objects",
			"Maximum allowed number of objects in each user bucket",
			userLabels, nil,
		),

		// System
		scrapeDurationSeconds: prometheus.NewDesc(
			"radosgw_usage_scrape_duration_seconds",
			"Amount of time each scrape takes",
			nil, nil,
		),
		up: prometheus.NewDesc(
			"radosgw_up",
			"Whether the RADOSGW exporter is able to communicate with RADOSGW.",
			nil, nil,
		),
	}
}

// Describe implements Collector
func (c *RADOSGWCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.ops
	ch <- c.successfulOps
	ch <- c.bytesSent
	ch <- c.bytesReceived
	ch <- c.bucketUsageBytes
	ch <- c.bucketUsageObjects
	ch <- c.userTotalBytes
	ch <- c.userTotalObjects
	ch <- c.userQuotaEnabled
	ch <- c.userQuotaMaxSizeBytes
	ch <- c.userQuotaMaxObjects
	ch <- c.userBucketQuotaEnabled
	ch <- c.userBucketQuotaMaxSizeBytes
	ch <- c.userBucketQuotaMaxObjects
	ch <- c.scrapeDurationSeconds
	ch <- c.up
}

// Collect implements Collector
func (c *RADOSGWCollector) Collect(ch chan<- prometheus.Metric) {
	start := time.Now()
	defer func() {
		duration := time.Since(start).Seconds()
		ch <- prometheus.MustNewConstMetric(c.scrapeDurationSeconds, prometheus.GaugeValue, duration)
		if duration > 10.0 {
			c.logger.Warn("Scrape took more than 10 seconds", "duration_sec", duration)
		}
	}()

	var up float64 = 1.0
	defer func() {
		ch <- prometheus.MustNewConstMetric(c.up, prometheus.GaugeValue, up)
	}()

	ctx := context.Background()

	// === Get Usage ===
	showEntries, showSummary := true, false
	usage, err := c.client.GetUsage(ctx, admin.Usage{
		ShowEntries: &showEntries,
		ShowSummary: &showSummary,
	})
	if err != nil {
		c.logger.Error("Failed to fetch usage from RADOSGW", "error", err)
		up = 0.0
		return
	}

	// Aggregate usage by unique key
	usageAggr := make(map[usageMetricKey]*usageMetricValues)
	for _, entry := range usage.Entries {
		user := entry.User
		for _, bucket := range entry.Buckets {
			bucketName := bucket.Bucket
			if bucketName == "" {
				bucketName = "bucket_root"
			}
			for _, cat := range bucket.Categories {
				key := usageMetricKey{
					bucket:   bucketName,
					owner:    user,
					category: cat.Category,
					store:    c.store,
				}
				if _, exists := usageAggr[key]; !exists {
					usageAggr[key] = &usageMetricValues{}
				}
				v := usageAggr[key]
				v.ops += float64(cat.Ops)
				v.successfulOps += float64(cat.SuccessfulOps)
				v.bytesSent += float64(cat.BytesSent)
				v.bytesReceived += float64(cat.BytesReceived)
			}
		}
	}

	// Emit usage metrics
	for key, vals := range usageAggr {
		labels := []string{key.bucket, key.owner, key.category, key.store}
		ch <- prometheus.MustNewConstMetric(c.ops, prometheus.CounterValue, vals.ops, labels...)
		ch <- prometheus.MustNewConstMetric(c.successfulOps, prometheus.CounterValue, vals.successfulOps, labels...)
		ch <- prometheus.MustNewConstMetric(c.bytesSent, prometheus.CounterValue, vals.bytesSent, labels...)
		ch <- prometheus.MustNewConstMetric(c.bytesReceived, prometheus.CounterValue, vals.bytesReceived, labels...)
	}

	// === Get all users ===
	uids, err := c.client.GetUsers(ctx)
	if err != nil {
		c.logger.Error("Failed to list users", "error", err)
		up = 0.0
		return
	}

	// === Process users and buckets ===
	for _, uid := range *uids {
		user, err := c.client.GetUser(ctx, admin.User{ID: uid})
		if err != nil {
			c.logger.Debug("Failed to get user details", "uid", uid, "error", err)
			continue
		}

		userLabels := []string{user.ID, c.store}

		// User totals
		if user.Stat.NumObjects != nil {
			ch <- prometheus.MustNewConstMetric(c.userTotalObjects, prometheus.GaugeValue, float64(*user.Stat.NumObjects), userLabels...)
		}
		if user.Stat.Size != nil {
			ch <- prometheus.MustNewConstMetric(c.userTotalBytes, prometheus.GaugeValue, float64(*user.Stat.Size), userLabels...)
		}

		// User Quota
		if user.UserQuota.Enabled != nil {
			enabled := 0.0
			if *user.UserQuota.Enabled {
				enabled = 1.0
			}
			ch <- prometheus.MustNewConstMetric(c.userQuotaEnabled, prometheus.GaugeValue, enabled, userLabels...)
		}
		if user.UserQuota.MaxSizeKb != nil {
			ch <- prometheus.MustNewConstMetric(c.userQuotaMaxSizeBytes, prometheus.GaugeValue, float64(*user.UserQuota.MaxSizeKb*1024), userLabels...)
		}
		if user.UserQuota.MaxObjects != nil {
			ch <- prometheus.MustNewConstMetric(c.userQuotaMaxObjects, prometheus.GaugeValue, float64(*user.UserQuota.MaxObjects), userLabels...)
		}

		// Bucket Quota (per-user)
		if user.BucketQuota.Enabled != nil {
			enabled := 0.0
			if *user.BucketQuota.Enabled {
				enabled = 1.0
			}
			ch <- prometheus.MustNewConstMetric(c.userBucketQuotaEnabled, prometheus.GaugeValue, enabled, userLabels...)
		}
		if user.BucketQuota.MaxSizeKb != nil {
			ch <- prometheus.MustNewConstMetric(c.userBucketQuotaMaxSizeBytes, prometheus.GaugeValue, float64(*user.BucketQuota.MaxSizeKb*1024), userLabels...)
		}
		if user.BucketQuota.MaxObjects != nil {
			ch <- prometheus.MustNewConstMetric(c.userBucketQuotaMaxObjects, prometheus.GaugeValue, float64(*user.BucketQuota.MaxObjects), userLabels...)
		}

		// Bucket stats
		buckets, err := c.client.ListUsersBucketsWithStat(ctx, uid)
		if err != nil {
			c.logger.Debug("Failed to list buckets for user", "uid", uid, "error", err)
			continue
		}
		for _, b := range buckets {
			bucketName := b.Bucket
			owner := b.Owner
			labels := []string{bucketName, owner, "bucket_total", c.store}

			if b.Usage.RgwMain.NumObjects != nil {
				ch <- prometheus.MustNewConstMetric(c.bucketUsageObjects, prometheus.GaugeValue, float64(*b.Usage.RgwMain.NumObjects), labels...)
			}
			if b.Usage.RgwMain.SizeActual != nil {
				ch <- prometheus.MustNewConstMetric(c.bucketUsageBytes, prometheus.GaugeValue, float64(*b.Usage.RgwMain.SizeActual), labels...)
			}
		}
	}
}
