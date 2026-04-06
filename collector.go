package main

import (
	"context"
	"crypto/tls"
	"log/slog"
	"net/http"
	"reflect"
	"time"

	"github.com/ceph/go-ceph/rgw/admin"
	"github.com/prometheus/client_golang/prometheus"
)

type usageMetricKey struct {
	bucket, owner, category string
}

type usagePoint struct {
	epoch         int64
	ops           float64
	successfulOps float64
	failedOps     float64
	bytesSent     float64
	bytesReceived float64
}

type s3SelectKey struct {
	bucket, owner string
}

type s3SelectPoint struct {
	epoch          int64
	bytesProcessed float64
	bytesReturned  float64
}

type RADOSGWCollector struct {
	client *admin.API
	logger *slog.Logger

	usageOps           *prometheus.Desc
	usageSuccessfulOps *prometheus.Desc
	usageFailedOps     *prometheus.Desc
	usageBytesSent     *prometheus.Desc
	usageBytesReceived *prometheus.Desc
	usageEpoch         *prometheus.Desc

	s3selectBytesProcessed *prometheus.Desc
	s3selectBytesReturned  *prometheus.Desc
	s3selectEpoch          *prometheus.Desc

	bucketUsageBytes   *prometheus.Desc
	bucketUsageObjects *prometheus.Desc

	bucketQuotaEnabled      *prometheus.Desc
	bucketQuotaMaxSizeBytes *prometheus.Desc
	bucketQuotaMaxObjects   *prometheus.Desc

	userTotalBytes   *prometheus.Desc
	userTotalObjects *prometheus.Desc

	userQuotaEnabled      *prometheus.Desc
	userQuotaMaxSizeBytes *prometheus.Desc
	userQuotaMaxObjects   *prometheus.Desc

	userBucketQuotaEnabled      *prometheus.Desc
	userBucketQuotaMaxSizeBytes *prometheus.Desc
	userBucketQuotaMaxObjects   *prometheus.Desc

	scrapeDurationSeconds *prometheus.Desc
	up                    *prometheus.Desc

	scrapeTimeout         time.Duration
	enableUserStats       bool
	enableBucketStats     bool
	enableUsageMetrics    bool
	enableS3SelectMetrics bool
}

func NewRADOSGWCollector(
	endpoint, accessKey, secretKey string,
	insecure bool,
	scrapeTimeout time.Duration,
	enableUserStats bool,
	enableBucketStats bool,
	enableUsageMetrics bool,
	enableS3SelectMetrics bool,
	logger *slog.Logger,
) *RADOSGWCollector {

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

	usageLabels := []string{"bucket", "owner", "category"}
	s3selectLabels := []string{"bucket", "owner"}
	bucketStatLabels := []string{"bucket", "owner", "usage_scope"}
	bucketQuotaLabels := []string{"bucket", "owner"}
	userLabels := []string{"user"}

	return &RADOSGWCollector{
		client: client,
		logger: logger,

		usageOps: prometheus.NewDesc(
			"radosgw_usage_ops",
			"Number of operations",
			usageLabels, nil,
		),
		usageSuccessfulOps: prometheus.NewDesc(
			"radosgw_usage_successful_ops",
			"Successful operations",
			usageLabels, nil,
		),
		usageFailedOps: prometheus.NewDesc(
			"radosgw_usage_failed_ops",
			"Failed operations",
			usageLabels, nil,
		),
		usageBytesSent: prometheus.NewDesc(
			"radosgw_usage_sent_bytes",
			"Bytes sent",
			usageLabels, nil,
		),
		usageBytesReceived: prometheus.NewDesc(
			"radosgw_usage_received_bytes",
			"Bytes received",
			usageLabels, nil,
		),
		usageEpoch: prometheus.NewDesc(
			"radosgw_usage_epoch",
			"Usage epoch",
			usageLabels, nil,
		),

		s3selectBytesProcessed: prometheus.NewDesc(
			"radosgw_s3select_bytes_processed",
			"S3Select bytes processed",
			s3selectLabels, nil,
		),
		s3selectBytesReturned: prometheus.NewDesc(
			"radosgw_s3select_bytes_returned",
			"S3Select bytes returned",
			s3selectLabels, nil,
		),
		s3selectEpoch: prometheus.NewDesc(
			"radosgw_s3select_epoch",
			"S3Select epoch",
			s3selectLabels, nil,
		),

		bucketUsageBytes: prometheus.NewDesc(
			"radosgw_bucket_usage_bytes",
			"Bucket used bytes",
			bucketStatLabels, nil,
		),
		bucketUsageObjects: prometheus.NewDesc(
			"radosgw_bucket_usage_objects",
			"Bucket object count",
			bucketStatLabels, nil,
		),

		bucketQuotaEnabled: prometheus.NewDesc(
			"radosgw_bucket_quota_enabled",
			"Bucket quota enabled",
			bucketQuotaLabels, nil,
		),
		bucketQuotaMaxSizeBytes: prometheus.NewDesc(
			"radosgw_bucket_quota_size_bytes",
			"Bucket max size",
			bucketQuotaLabels, nil,
		),
		bucketQuotaMaxObjects: prometheus.NewDesc(
			"radosgw_bucket_quota_objects",
			"Bucket max objects",
			bucketQuotaLabels, nil,
		),

		userTotalBytes: prometheus.NewDesc(
			"radosgw_user_total_bytes",
			"User total bytes",
			userLabels, nil,
		),
		userTotalObjects: prometheus.NewDesc(
			"radosgw_user_total_objects",
			"User total objects",
			userLabels, nil,
		),

		userQuotaEnabled: prometheus.NewDesc(
			"radosgw_user_quota_enabled",
			"User quota enabled",
			userLabels, nil,
		),
		userQuotaMaxSizeBytes: prometheus.NewDesc(
			"radosgw_user_quota_size_bytes",
			"User max size",
			userLabels, nil,
		),
		userQuotaMaxObjects: prometheus.NewDesc(
			"radosgw_user_quota_objects",
			"User max objects",
			userLabels, nil,
		),

		userBucketQuotaEnabled: prometheus.NewDesc(
			"radosgw_user_bucket_quota_enabled",
			"User bucket quota enabled",
			userLabels, nil,
		),
		userBucketQuotaMaxSizeBytes: prometheus.NewDesc(
			"radosgw_user_bucket_quota_size_bytes",
			"User bucket max size",
			userLabels, nil,
		),
		userBucketQuotaMaxObjects: prometheus.NewDesc(
			"radosgw_user_bucket_quota_objects",
			"User bucket max objects",
			userLabels, nil,
		),

		scrapeDurationSeconds: prometheus.NewDesc(
			"radosgw_scrape_duration_seconds",
			"Scrape duration",
			nil, nil,
		),

		up: prometheus.NewDesc(
			"radosgw_up",
			"Exporter availability",
			nil, nil,
		),

		scrapeTimeout:         scrapeTimeout,
		enableUserStats:       enableUserStats,
		enableBucketStats:     enableBucketStats,
		enableUsageMetrics:    enableUsageMetrics,
		enableS3SelectMetrics: enableS3SelectMetrics,
	}
}

func (c *RADOSGWCollector) Describe(ch chan<- *prometheus.Desc) {

	ch <- c.scrapeDurationSeconds
	ch <- c.up

	if c.enableUsageMetrics {
		ch <- c.usageOps
		ch <- c.usageSuccessfulOps
		ch <- c.usageFailedOps
		ch <- c.usageBytesSent
		ch <- c.usageBytesReceived
		ch <- c.usageEpoch
	}

	if c.enableS3SelectMetrics {
		ch <- c.s3selectBytesProcessed
		ch <- c.s3selectBytesReturned
		ch <- c.s3selectEpoch
	}

	if c.enableUserStats {
		ch <- c.userTotalBytes
		ch <- c.userTotalObjects
		ch <- c.userQuotaEnabled
		ch <- c.userQuotaMaxSizeBytes
		ch <- c.userQuotaMaxObjects
		ch <- c.userBucketQuotaEnabled
		ch <- c.userBucketQuotaMaxSizeBytes
		ch <- c.userBucketQuotaMaxObjects
	}

	if c.enableBucketStats {
		ch <- c.bucketUsageBytes
		ch <- c.bucketUsageObjects
		ch <- c.bucketQuotaEnabled
		ch <- c.bucketQuotaMaxSizeBytes
		ch <- c.bucketQuotaMaxObjects
	}
}

func (c *RADOSGWCollector) Collect(ch chan<- prometheus.Metric) {

	start := time.Now()

	defer func() {
		ch <- prometheus.MustNewConstMetric(
			c.scrapeDurationSeconds, prometheus.GaugeValue,
			time.Since(start).Seconds(),
		)
	}()

	up := 1.0
	defer func() {
		ch <- prometheus.MustNewConstMetric(c.up, prometheus.GaugeValue, up)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), c.scrapeTimeout)
	defer cancel()

	showEntries, showSummary := true, false
	usage, err := c.client.GetUsage(ctx, admin.Usage{
		ShowEntries: &showEntries,
		ShowSummary: &showSummary,
	})

	if err != nil {
		c.logger.Error("Failed to fetch usage", "error", err)
		up = 0
		return
	}

	latestUsage := make(map[usageMetricKey]usagePoint)
	latestS3 := make(map[s3SelectKey]s3SelectPoint)

	for _, entry := range usage.Entries {

		entryUser := entry.User

		for _, b := range entry.Buckets {

			bucketName := normalizeBucketName(b.Bucket)
			owner := entryUser

			if o := readStringField(b, "Owner"); o != "" {
				owner = o
			}

			epoch := readInt64Field(b, "Epoch")

			if c.enableS3SelectMetrics {
				s3Processed, s3Returned := readS3Select(b)
				sk := s3SelectKey{bucket: bucketName, owner: owner}
				prev := latestS3[sk]
				if epoch >= prev.epoch {
					latestS3[sk] = s3SelectPoint{
						epoch:          epoch,
						bytesProcessed: s3Processed,
						bytesReturned:  s3Returned,
					}
				}
			}

			if c.enableUsageMetrics {

				for _, cat := range b.Categories {
					k := usageMetricKey{
						bucket:   bucketName,
						owner:    owner,
						category: cat.Category,
					}

					prev := latestUsage[k]
					if epoch < prev.epoch {
						continue
					}

					ops := float64(cat.Ops)
					succ := float64(cat.SuccessfulOps)
					failed := ops - succ
					if failed < 0 {
						failed = 0
					}

					latestUsage[k] = usagePoint{
						epoch:         epoch,
						ops:           ops,
						successfulOps: succ,
						failedOps:     failed,
						bytesSent:     float64(cat.BytesSent),
						bytesReceived: float64(cat.BytesReceived),
					}
				}
			}
		}
	}

	if c.enableUsageMetrics {
		for k, p := range latestUsage {
			labels := []string{k.bucket, k.owner, k.category}
			ch <- prometheus.MustNewConstMetric(c.usageOps, prometheus.GaugeValue, p.ops, labels...)
			ch <- prometheus.MustNewConstMetric(c.usageSuccessfulOps, prometheus.GaugeValue, p.successfulOps, labels...)
			ch <- prometheus.MustNewConstMetric(c.usageFailedOps, prometheus.GaugeValue, p.failedOps, labels...)
			ch <- prometheus.MustNewConstMetric(c.usageBytesSent, prometheus.GaugeValue, p.bytesSent, labels...)
			ch <- prometheus.MustNewConstMetric(c.usageBytesReceived, prometheus.GaugeValue, p.bytesReceived, labels...)
			ch <- prometheus.MustNewConstMetric(c.usageEpoch, prometheus.GaugeValue, float64(p.epoch), labels...)
		}
	}

	if c.enableS3SelectMetrics {
		for k, p := range latestS3 {
			labels := []string{k.bucket, k.owner}
			ch <- prometheus.MustNewConstMetric(c.s3selectBytesProcessed, prometheus.GaugeValue, p.bytesProcessed, labels...)
			ch <- prometheus.MustNewConstMetric(c.s3selectBytesReturned, prometheus.GaugeValue, p.bytesReturned, labels...)
			ch <- prometheus.MustNewConstMetric(c.s3selectEpoch, prometheus.GaugeValue, float64(p.epoch), labels...)
		}
	}

	if !c.enableUserStats {
		return
	}

	uids, err := c.client.GetUsers(ctx)
	if err != nil {
		c.logger.Error("Failed to list users", "error", err)
		up = 0
		return
	}
	if uids == nil {
		c.logger.Error("GetUsers returned nil slice")
		up = 0
		return
	}

	processedUserIDs := make(map[string]bool)

	stats := true

	for _, uid := range *uids {

		user, err := c.client.GetUser(ctx, admin.User{
			ID:           uid,
			GenerateStat: &stats,
		})

		if err != nil {
			c.logger.Debug("Failed to get user", "uid", uid, "error", err)
			continue
		}

		if processedUserIDs[user.ID] {
			continue
		}

		processedUserIDs[user.ID] = true

		userLabels := []string{user.ID}

		if user.Stat.NumObjects != nil {
			ch <- prometheus.MustNewConstMetric(
				c.userTotalObjects, prometheus.GaugeValue,
				float64(*user.Stat.NumObjects),
				userLabels...,
			)
		}

		if user.Stat.Size != nil {
			ch <- prometheus.MustNewConstMetric(
				c.userTotalBytes, prometheus.GaugeValue,
				float64(*user.Stat.Size),
				userLabels...,
			)
		}

		if user.UserQuota.Enabled != nil {
			val := 0.0
			if *user.UserQuota.Enabled {
				val = 1
			}
			ch <- prometheus.MustNewConstMetric(
				c.userQuotaEnabled, prometheus.GaugeValue,
				val, userLabels...,
			)
		}

		if user.UserQuota.MaxSizeKb != nil {
			ch <- prometheus.MustNewConstMetric(
				c.userQuotaMaxSizeBytes, prometheus.GaugeValue,
				float64(*user.UserQuota.MaxSizeKb*1024),
				userLabels...,
			)
		}

		if user.UserQuota.MaxObjects != nil {
			ch <- prometheus.MustNewConstMetric(
				c.userQuotaMaxObjects, prometheus.GaugeValue,
				float64(*user.UserQuota.MaxObjects),
				userLabels...,
			)
		}

		if user.BucketQuota.Enabled != nil {
			val := 0.0
			if *user.BucketQuota.Enabled {
				val = 1
			}
			ch <- prometheus.MustNewConstMetric(
				c.userBucketQuotaEnabled, prometheus.GaugeValue,
				val, userLabels...,
			)
		}

		if user.BucketQuota.MaxSizeKb != nil {
			ch <- prometheus.MustNewConstMetric(
				c.userBucketQuotaMaxSizeBytes, prometheus.GaugeValue,
				float64(*user.BucketQuota.MaxSizeKb*1024),
				userLabels...,
			)
		}

		if user.BucketQuota.MaxObjects != nil {
			ch <- prometheus.MustNewConstMetric(
				c.userBucketQuotaMaxObjects, prometheus.GaugeValue,
				float64(*user.BucketQuota.MaxObjects),
				userLabels...,
			)
		}

		if !c.enableBucketStats {
			continue
		}

		buckets, err := c.client.ListUsersBucketsWithStat(ctx, uid)
		if err != nil {
			c.logger.Debug("Failed to list buckets", "uid", uid, "error", err)
			continue
		}

		for _, b := range buckets {

			bucketName := b.Bucket
			owner := b.Owner

			if b.Usage.RgwMain.NumObjects != nil {
				ch <- prometheus.MustNewConstMetric(
					c.bucketUsageObjects, prometheus.GaugeValue,
					float64(*b.Usage.RgwMain.NumObjects),
					bucketName, owner, "rgw.main",
				)
			}

			if b.Usage.RgwMain.SizeActual != nil {
				ch <- prometheus.MustNewConstMetric(
					c.bucketUsageBytes, prometheus.GaugeValue,
					float64(*b.Usage.RgwMain.SizeActual),
					bucketName, owner, "rgw.main",
				)
			}

			if b.Usage.RgwMultimeta.NumObjects != nil {
				ch <- prometheus.MustNewConstMetric(
					c.bucketUsageObjects, prometheus.GaugeValue,
					float64(*b.Usage.RgwMultimeta.NumObjects),
					bucketName, owner, "rgw.multimeta",
				)
			}

			if b.Usage.RgwMultimeta.SizeActual != nil {
				ch <- prometheus.MustNewConstMetric(
					c.bucketUsageBytes, prometheus.GaugeValue,
					float64(*b.Usage.RgwMultimeta.SizeActual),
					bucketName, owner, "rgw.multimeta",
				)
			}

			quotaLabels := []string{bucketName, owner}

			if b.BucketQuota.Enabled != nil {
				val := 0.0
				if *b.BucketQuota.Enabled {
					val = 1
				}
				ch <- prometheus.MustNewConstMetric(
					c.bucketQuotaEnabled, prometheus.GaugeValue,
					val, quotaLabels...,
				)
			}

			if b.BucketQuota.MaxSizeKb != nil {
				ch <- prometheus.MustNewConstMetric(
					c.bucketQuotaMaxSizeBytes, prometheus.GaugeValue,
					float64(*b.BucketQuota.MaxSizeKb*1024),
					quotaLabels...,
				)
			}

			if b.BucketQuota.MaxObjects != nil {
				ch <- prometheus.MustNewConstMetric(
					c.bucketQuotaMaxObjects, prometheus.GaugeValue,
					float64(*b.BucketQuota.MaxObjects),
					quotaLabels...,
				)
			}
		}
	}
}

func normalizeBucketName(s string) string {
	if s == "" || s == "-" {
		return "bucket_unknown"
	}
	return s
}

func readStringField(x any, field string) string {
	v := reflect.ValueOf(x)
	for v.IsValid() && v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return ""
		}
		v = v.Elem()
	}
	if !v.IsValid() || v.Kind() != reflect.Struct {
		return ""
	}
	f := v.FieldByName(field)
	if !f.IsValid() {
		return ""
	}
	for f.IsValid() && f.Kind() == reflect.Pointer {
		if f.IsNil() {
			return ""
		}
		f = f.Elem()
	}
	if f.Kind() == reflect.String {
		return f.String()
	}
	return ""
}

func readInt64Field(x any, field string) int64 {
	v := reflect.ValueOf(x)
	for v.IsValid() && v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return 0
		}
		v = v.Elem()
	}
	if !v.IsValid() || v.Kind() != reflect.Struct {
		return 0
	}
	f := v.FieldByName(field)
	if !f.IsValid() {
		return 0
	}
	for f.IsValid() && f.Kind() == reflect.Pointer {
		if f.IsNil() {
			return 0
		}
		f = f.Elem()
	}
	switch f.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return f.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return int64(f.Uint())
	case reflect.Float32, reflect.Float64:
		return int64(f.Float())
	default:
		return 0
	}
}

func readS3Select(bucket any) (float64, float64) {
	v := reflect.ValueOf(bucket)
	for v.IsValid() && v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return 0, 0
		}
		v = v.Elem()
	}
	if !v.IsValid() || v.Kind() != reflect.Struct {
		return 0, 0
	}

	var s3 reflect.Value
	for _, name := range []string{"S3select", "S3Select"} {
		f := v.FieldByName(name)
		if f.IsValid() {
			s3 = f
			break
		}
	}
	if !s3.IsValid() {
		return 0, 0
	}
	for s3.IsValid() && s3.Kind() == reflect.Pointer {
		if s3.IsNil() {
			return 0, 0
		}
		s3 = s3.Elem()
	}
	if !s3.IsValid() || s3.Kind() != reflect.Struct {
		return 0, 0
	}

	processed := float64(readInt64Field(s3.Interface(), "BytesProcessed"))
	returned := float64(readInt64Field(s3.Interface(), "BytesReturned"))
	return processed, returned
}
