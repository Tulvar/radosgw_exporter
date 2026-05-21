package main

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ceph/go-ceph/rgw/admin"
	"github.com/prometheus/client_golang/prometheus"
)

type usageMetricKey struct {
	bucket, owner, category string
}

type usagePoint struct {
	epoch         uint64
	ops           float64
	successfulOps float64
	failedOps     float64
	bytesSent     float64
	bytesReceived float64
}

type requestErrorKey struct {
	op     string
	reason string
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
	usageCacheAge      *prometheus.Desc
	usageCacheStale    *prometheus.Desc
	requestDuration    *prometheus.Desc
	requestErrorsTotal *prometheus.Desc
	requestSlowdown    *prometheus.Desc
	requestLastSuccess *prometheus.Desc
	usersCacheAge      *prometheus.Desc
	usersCacheStale    *prometheus.Desc

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

	scrapeTimeout        time.Duration
	usageCacheTTL        time.Duration
	usersBucketsCacheTTL time.Duration
	enableUserStats      bool
	enableBucketStats    bool
	enableUsageMetrics   bool
	maxUsersPerScrape    int

	mu                sync.RWMutex
	cachedUsage       map[usageMetricKey]usagePoint
	usageCacheUpdated time.Time
	cachedUsers       map[string]admin.User
	cachedBuckets     map[string][]admin.Bucket
	usersCacheUpdated time.Time
	userCursor        int

	obsMu           sync.RWMutex
	lastRequestDur  map[string]float64
	requestErrors   map[requestErrorKey]uint64
	slowdownTotal   map[string]uint64
	lastSuccessUnix map[string]float64
}

func NewRADOSGWCollector(
	endpoint, accessKey, secretKey string,
	insecure bool,
	scrapeTimeout time.Duration,
	usageCacheTTL time.Duration,
	usersBucketsCacheTTL time.Duration,
	enableUserStats bool,
	enableBucketStats bool,
	enableUsageMetrics bool,
	maxUsersPerScrape int,
	logger *slog.Logger,
) (*RADOSGWCollector, error) {

	httpClient := &http.Client{
		Timeout: scrapeTimeout + 5*time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: insecure,
			},
		},
	}

	client, err := admin.New(endpoint, accessKey, secretKey, httpClient)
	if err != nil {
		return nil, err
	}

	usageLabels := []string{"bucket", "owner", "category"}
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
		usageCacheAge: prometheus.NewDesc(
			"radosgw_usage_cache_age_seconds",
			"Age of last successful usage cache update in seconds",
			nil, nil,
		),
		usageCacheStale: prometheus.NewDesc(
			"radosgw_usage_cache_stale",
			"Whether usage metrics are served from stale cache (1=true, 0=false)",
			nil, nil,
		),
		requestDuration: prometheus.NewDesc(
			"radosgw_exporter_request_duration_seconds",
			"Last request duration in seconds by RGW admin operation",
			[]string{"op"}, nil,
		),
		requestErrorsTotal: prometheus.NewDesc(
			"radosgw_exporter_request_errors_total",
			"Total number of request errors by operation and reason",
			[]string{"op", "reason"}, nil,
		),
		requestSlowdown: prometheus.NewDesc(
			"radosgw_exporter_slowdown_total",
			"Total number of SlowDown errors by operation",
			[]string{"op"}, nil,
		),
		requestLastSuccess: prometheus.NewDesc(
			"radosgw_exporter_last_success_unixtime",
			"Unix timestamp of last successful request by operation",
			[]string{"op"}, nil,
		),
		usersCacheAge: prometheus.NewDesc(
			"radosgw_users_buckets_cache_age_seconds",
			"Age of last successful users/buckets cache update in seconds",
			nil, nil,
		),
		usersCacheStale: prometheus.NewDesc(
			"radosgw_users_buckets_cache_stale",
			"Whether users/buckets metrics are served from stale cache (1=true, 0=false)",
			nil, nil,
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

		scrapeTimeout:        scrapeTimeout,
		usageCacheTTL:        usageCacheTTL,
		usersBucketsCacheTTL: usersBucketsCacheTTL,
		enableUserStats:      enableUserStats,
		enableBucketStats:    enableBucketStats,
		enableUsageMetrics:   enableUsageMetrics,
		maxUsersPerScrape:    maxUsersPerScrape,
		cachedUsage:          make(map[usageMetricKey]usagePoint),
		cachedUsers:          make(map[string]admin.User),
		cachedBuckets:        make(map[string][]admin.Bucket),
		lastRequestDur:       make(map[string]float64),
		requestErrors:        make(map[requestErrorKey]uint64),
		slowdownTotal:        make(map[string]uint64),
		lastSuccessUnix:      make(map[string]float64),
	}, nil
}

func (c *RADOSGWCollector) Describe(ch chan<- *prometheus.Desc) {

	ch <- c.scrapeDurationSeconds
	ch <- c.up
	ch <- c.requestDuration
	ch <- c.requestErrorsTotal
	ch <- c.requestSlowdown
	ch <- c.requestLastSuccess

	if c.enableUsageMetrics {
		ch <- c.usageOps
		ch <- c.usageSuccessfulOps
		ch <- c.usageFailedOps
		ch <- c.usageBytesSent
		ch <- c.usageBytesReceived
		ch <- c.usageEpoch
		ch <- c.usageCacheAge
		ch <- c.usageCacheStale
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
	if c.enableUserStats || c.enableBucketStats {
		ch <- c.usersCacheAge
		ch <- c.usersCacheStale
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
	defer c.collectInternalTelemetry(ch)

	baseCtx := context.Background()

	latestUsage := make(map[usageMetricKey]usagePoint)
	usageStale := 0.0
	if c.enableUsageMetrics {
		if cachedUsage, ok := c.getCachedUsageWithinTTL(time.Now()); ok {
			latestUsage = cachedUsage
		} else {
			usageCtx, usageCancel := context.WithTimeout(baseCtx, c.scrapeTimeout)
			showEntries, showSummary := true, false
			usage, err := c.getUsageWithRetry(usageCtx, admin.Usage{
				ShowEntries: &showEntries,
				ShowSummary: &showSummary,
			})
			usageCancel()

			if err != nil {
				c.logger.Error("Failed to fetch usage", "error", err)

				if isTransientUsageError(err) {
					cachedUsage, hasCache := c.getCachedUsage()
					if hasCache {
						latestUsage = cachedUsage
						usageStale = 1
						c.logger.Warn("Using stale cached usage metrics", "cache_entries", len(cachedUsage))
					} else {
						c.logger.Warn("Usage metrics unavailable and cache is empty")
					}
				} else {
					up = 0
					return
				}
			} else {
				for _, entry := range usage.Entries {
					entryUser := entry.User

					for _, b := range entry.Buckets {
						bucketName := normalizeBucketName(b.Bucket)
						owner := entryUser
						if b.Owner != "" {
							owner = b.Owner
						}

						epoch := b.Epoch

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
				c.setCachedUsage(latestUsage)
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
		cacheAge := c.usageCacheAgeSeconds(time.Now())
		ch <- prometheus.MustNewConstMetric(c.usageCacheAge, prometheus.GaugeValue, cacheAge)
		ch <- prometheus.MustNewConstMetric(c.usageCacheStale, prometheus.GaugeValue, usageStale)
	}

	if !c.enableUserStats && !c.enableBucketStats {
		return
	}

	usersStale := 0.0
	usersTimedOut := false
	usersTotal := 0
	usersSelected := 0
	usersFetched := 0
	usersFetchFailed := 0
	bucketsFetched := 0
	bucketsFetchFailed := 0

	if !c.hasUsersBucketsCacheWithinTTL(time.Now()) {
		usersPhaseCtx, usersPhaseCancel := context.WithTimeout(baseCtx, c.scrapeTimeout)
		defer usersPhaseCancel()

		usersCallStart := time.Now()
		uids, err := c.client.GetUsers(usersPhaseCtx)
		c.observeRequest("users", usersCallStart, err)
		if err != nil {
			c.logger.Error("Failed to list users", "error", err)
			if isTransientUsageError(err) && c.hasUsersBucketsCache() {
				usersStale = 1
			} else {
				up = 0
				return
			}
		}
		if uids != nil {
			usersTotal = len(*uids)
			c.pruneUsersBucketsCacheByUIDs(*uids)
		}
		if uids == nil && usersStale == 0 {
			c.logger.Error("GetUsers returned nil slice")
			up = 0
			return
		}

		var generateStat *bool
		if c.enableUserStats {
			stats := true
			generateStat = &stats
		}

		selectedUIDs := c.pickUserBatch(uids)
		usersSelected = len(selectedUIDs)

		for _, uid := range selectedUIDs {
			if usersPhaseCtx.Err() != nil {
				c.logger.Warn("Users/buckets collection stopped due to phase timeout")
				usersStale = 1
				usersTimedOut = true
				break
			}

			userCallStart := time.Now()
			user, err := c.client.GetUser(usersPhaseCtx, admin.User{
				ID:           uid,
				GenerateStat: generateStat,
			})
			c.observeRequest("user", userCallStart, err)

			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
					c.logger.Warn("Users/buckets collection stopped due to context cancellation", "uid", uid, "error", err)
					usersStale = 1
					usersTimedOut = true
					break
				}
				c.logger.Debug("Failed to get user", "uid", uid, "error", err)
				usersStale = 1
				usersFetchFailed++
				continue
			}
			usersFetched++
			c.setCachedUser(uid, user)

			if !c.enableBucketStats {
				continue
			}

			bucketsCallStart := time.Now()
			buckets, err := c.client.ListUsersBucketsWithStat(usersPhaseCtx, uid)
			c.observeRequest("buckets", bucketsCallStart, err)
			if err != nil {
				if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
					c.logger.Warn("Users/buckets collection stopped while listing buckets due to context cancellation", "uid", uid, "error", err)
					usersStale = 1
					usersTimedOut = true
					break
				}
				c.logger.Debug("Failed to list buckets", "uid", uid, "error", err)
				usersStale = 1
				bucketsFetchFailed++
				continue
			}
			bucketsFetched++
			c.setCachedBuckets(uid, buckets)
		}

		if uids != nil && (usersFetched > 0 || bucketsFetched > 0 || (!c.enableBucketStats && usersFetched > 0)) {
			c.touchUsersBucketsCacheUpdated()
		}
	}

	cachedUsers := c.getCachedUsers()
	cachedBuckets := c.getCachedBuckets()

	if c.enableUserStats {
		type aggregatedUserMetrics struct {
			totalObjects          float64
			hasTotalObjects       bool
			totalBytes            float64
			hasTotalBytes         bool
			userQuotaEnabled      bool
			hasUserQuotaEnabled   bool
			userQuotaMaxSizeBytes float64
			hasUserQuotaMaxSize   bool
			userQuotaMaxObjects   float64
			hasUserQuotaMaxObj    bool

			bucketQuotaEnabled      bool
			hasBucketQuotaEnabled   bool
			bucketQuotaMaxSizeBytes float64
			hasBucketQuotaMaxSize   bool
			bucketQuotaMaxObjects   float64
			hasBucketQuotaMaxObj    bool
		}

		aggregatedByUser := make(map[string]aggregatedUserMetrics, len(cachedUsers))
		for uid, user := range cachedUsers {
			userID := user.ID
			if userID == "" {
				userID = uid
			}
			if userID == "" {
				continue
			}
			agg := aggregatedByUser[userID]

			if user.Stat.NumObjects != nil {
				agg.totalObjects += float64(*user.Stat.NumObjects)
				agg.hasTotalObjects = true
			}

			if user.Stat.Size != nil {
				agg.totalBytes += float64(*user.Stat.Size)
				agg.hasTotalBytes = true
			}

			if user.UserQuota.Enabled != nil {
				if *user.UserQuota.Enabled {
					agg.userQuotaEnabled = true
				}
				agg.hasUserQuotaEnabled = true
			}

			if user.UserQuota.MaxSizeKb != nil {
				value := float64(*user.UserQuota.MaxSizeKb * 1024)
				if !agg.hasUserQuotaMaxSize || value > agg.userQuotaMaxSizeBytes {
					agg.userQuotaMaxSizeBytes = value
				}
				agg.hasUserQuotaMaxSize = true
			}

			if user.UserQuota.MaxObjects != nil {
				value := float64(*user.UserQuota.MaxObjects)
				if !agg.hasUserQuotaMaxObj || value > agg.userQuotaMaxObjects {
					agg.userQuotaMaxObjects = value
				}
				agg.hasUserQuotaMaxObj = true
			}

			if user.BucketQuota.Enabled != nil {
				if *user.BucketQuota.Enabled {
					agg.bucketQuotaEnabled = true
				}
				agg.hasBucketQuotaEnabled = true
			}

			if user.BucketQuota.MaxSizeKb != nil {
				value := float64(*user.BucketQuota.MaxSizeKb * 1024)
				if !agg.hasBucketQuotaMaxSize || value > agg.bucketQuotaMaxSizeBytes {
					agg.bucketQuotaMaxSizeBytes = value
				}
				agg.hasBucketQuotaMaxSize = true
			}

			if user.BucketQuota.MaxObjects != nil {
				value := float64(*user.BucketQuota.MaxObjects)
				if !agg.hasBucketQuotaMaxObj || value > agg.bucketQuotaMaxObjects {
					agg.bucketQuotaMaxObjects = value
				}
				agg.hasBucketQuotaMaxObj = true
			}
			aggregatedByUser[userID] = agg
		}

		for userID, agg := range aggregatedByUser {
			userLabels := []string{userID}

			if agg.hasTotalObjects {
				ch <- prometheus.MustNewConstMetric(
					c.userTotalObjects, prometheus.GaugeValue,
					agg.totalObjects,
					userLabels...,
				)
			}

			if agg.hasTotalBytes {
				ch <- prometheus.MustNewConstMetric(
					c.userTotalBytes, prometheus.GaugeValue,
					agg.totalBytes,
					userLabels...,
				)
			}

			if agg.hasUserQuotaEnabled {
				val := 0.0
				if agg.userQuotaEnabled {
					val = 1
				}
				ch <- prometheus.MustNewConstMetric(
					c.userQuotaEnabled, prometheus.GaugeValue,
					val, userLabels...,
				)
			}

			if agg.hasUserQuotaMaxSize {
				ch <- prometheus.MustNewConstMetric(
					c.userQuotaMaxSizeBytes, prometheus.GaugeValue,
					agg.userQuotaMaxSizeBytes,
					userLabels...,
				)
			}

			if agg.hasUserQuotaMaxObj {
				ch <- prometheus.MustNewConstMetric(
					c.userQuotaMaxObjects, prometheus.GaugeValue,
					agg.userQuotaMaxObjects,
					userLabels...,
				)
			}

			if agg.hasBucketQuotaEnabled {
				val := 0.0
				if agg.bucketQuotaEnabled {
					val = 1
				}
				ch <- prometheus.MustNewConstMetric(
					c.userBucketQuotaEnabled, prometheus.GaugeValue,
					val, userLabels...,
				)
			}

			if agg.hasBucketQuotaMaxSize {
				ch <- prometheus.MustNewConstMetric(
					c.userBucketQuotaMaxSizeBytes, prometheus.GaugeValue,
					agg.bucketQuotaMaxSizeBytes,
					userLabels...,
				)
			}

			if agg.hasBucketQuotaMaxObj {
				ch <- prometheus.MustNewConstMetric(
					c.userBucketQuotaMaxObjects, prometheus.GaugeValue,
					agg.bucketQuotaMaxObjects,
					userLabels...,
				)
			}
		}
	}

	if c.enableBucketStats {
		for _, buckets := range cachedBuckets {
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

	if c.enableUserStats || c.enableBucketStats {
		ch <- prometheus.MustNewConstMetric(
			c.usersCacheAge,
			prometheus.GaugeValue,
			c.usersBucketsCacheAgeSeconds(time.Now()),
		)
		ch <- prometheus.MustNewConstMetric(c.usersCacheStale, prometheus.GaugeValue, usersStale)
	}

	if usersTimedOut || usersFetchFailed > 0 || bucketsFetchFailed > 0 {
		c.logger.Warn(
			"Users/buckets scrape summary",
			"users_total", usersTotal,
			"users_selected", usersSelected,
			"users_fetched", usersFetched,
			"users_failed", usersFetchFailed,
			"buckets_fetched", bucketsFetched,
			"buckets_failed", bucketsFetchFailed,
			"stale", usersStale,
			"phase_timeout", usersTimedOut,
		)
	}
}

func normalizeBucketName(s string) string {
	if s == "" || s == "-" {
		return "bucket_unknown"
	}
	return s
}

func (c *RADOSGWCollector) getUsageWithRetry(ctx context.Context, usage admin.Usage) (admin.Usage, error) {
	const maxAttempts = 3
	backoff := 200 * time.Millisecond

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		usageCallStart := time.Now()
		result, err := c.client.GetUsage(ctx, usage)
		c.observeRequest("usage", usageCallStart, err)
		if err == nil {
			if attempt > 1 {
				c.logger.Warn("Usage fetch recovered after retry", "attempt", attempt)
			}
			return result, nil
		}

		lastErr = err
		if !isSlowDownError(err) || attempt == maxAttempts {
			return admin.Usage{}, err
		}

		c.logger.Warn("Usage fetch got SlowDown, retrying", "attempt", attempt, "backoff", backoff.String())
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return admin.Usage{}, ctx.Err()
		case <-timer.C:
		}
		backoff *= 2
	}

	return admin.Usage{}, lastErr
}

func isSlowDownError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "SlowDown")
}

func isTransientUsageError(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, context.DeadlineExceeded) || isSlowDownError(err)
}

func (c *RADOSGWCollector) setCachedUsage(in map[usageMetricKey]usagePoint) {
	copyMap := make(map[usageMetricKey]usagePoint, len(in))
	for k, v := range in {
		copyMap[k] = v
	}

	c.mu.Lock()
	c.cachedUsage = copyMap
	c.usageCacheUpdated = time.Now()
	c.mu.Unlock()
}

func (c *RADOSGWCollector) getCachedUsage() (map[usageMetricKey]usagePoint, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.usageCacheUpdated.IsZero() || len(c.cachedUsage) == 0 {
		return nil, false
	}

	copyMap := make(map[usageMetricKey]usagePoint, len(c.cachedUsage))
	for k, v := range c.cachedUsage {
		copyMap[k] = v
	}
	return copyMap, true
}

func (c *RADOSGWCollector) getCachedUsageWithinTTL(now time.Time) (map[usageMetricKey]usagePoint, bool) {
	if c.usageCacheTTL <= 0 {
		return nil, false
	}

	c.mu.RLock()
	cacheUpdated := c.usageCacheUpdated
	cacheLen := len(c.cachedUsage)
	c.mu.RUnlock()

	if cacheUpdated.IsZero() || cacheLen == 0 {
		return nil, false
	}
	if now.Sub(cacheUpdated) > c.usageCacheTTL {
		return nil, false
	}

	return c.getCachedUsage()
}

func (c *RADOSGWCollector) usageCacheAgeSeconds(now time.Time) float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.usageCacheUpdated.IsZero() {
		return -1
	}
	return now.Sub(c.usageCacheUpdated).Seconds()
}

func (c *RADOSGWCollector) hasUsersBucketsCache() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.cachedUsers) > 0 || len(c.cachedBuckets) > 0
}

func (c *RADOSGWCollector) hasUsersBucketsCacheWithinTTL(now time.Time) bool {
	if c.usersBucketsCacheTTL <= 0 {
		return false
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.usersCacheUpdated.IsZero() {
		return false
	}
	if len(c.cachedUsers) == 0 && len(c.cachedBuckets) == 0 {
		return false
	}
	return now.Sub(c.usersCacheUpdated) <= c.usersBucketsCacheTTL
}

func (c *RADOSGWCollector) setCachedUser(uid string, user admin.User) {
	c.mu.Lock()
	c.cachedUsers[uid] = user
	c.mu.Unlock()
}

func (c *RADOSGWCollector) setCachedBuckets(uid string, buckets []admin.Bucket) {
	copyBuckets := make([]admin.Bucket, len(buckets))
	copy(copyBuckets, buckets)
	c.mu.Lock()
	c.cachedBuckets[uid] = copyBuckets
	c.mu.Unlock()
}

func (c *RADOSGWCollector) getCachedUsers() map[string]admin.User {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[string]admin.User, len(c.cachedUsers))
	for k, v := range c.cachedUsers {
		out[k] = v
	}
	return out
}

func (c *RADOSGWCollector) getCachedBuckets() map[string][]admin.Bucket {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[string][]admin.Bucket, len(c.cachedBuckets))
	for k, v := range c.cachedBuckets {
		copyBuckets := make([]admin.Bucket, len(v))
		copy(copyBuckets, v)
		out[k] = copyBuckets
	}
	return out
}

func (c *RADOSGWCollector) touchUsersBucketsCacheUpdated() {
	c.mu.Lock()
	c.usersCacheUpdated = time.Now()
	c.mu.Unlock()
}

func (c *RADOSGWCollector) usersBucketsCacheAgeSeconds(now time.Time) float64 {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.usersCacheUpdated.IsZero() {
		return -1
	}
	return now.Sub(c.usersCacheUpdated).Seconds()
}

func (c *RADOSGWCollector) pruneUsersBucketsCacheByUIDs(uids []string) {
	valid := make(map[string]struct{}, len(uids))
	for _, uid := range uids {
		valid[uid] = struct{}{}
	}

	c.mu.Lock()
	for uid := range c.cachedUsers {
		if _, ok := valid[uid]; !ok {
			delete(c.cachedUsers, uid)
		}
	}
	for uid := range c.cachedBuckets {
		if _, ok := valid[uid]; !ok {
			delete(c.cachedBuckets, uid)
		}
	}
	c.mu.Unlock()
}

func (c *RADOSGWCollector) pickUserBatch(uids *[]string) []string {
	if uids == nil || len(*uids) == 0 {
		return nil
	}
	all := *uids
	limit := c.maxUsersPerScrape
	if limit <= 0 || limit >= len(all) {
		return all
	}

	c.mu.Lock()
	start := c.userCursor % len(all)
	c.userCursor = (c.userCursor + limit) % len(all)
	c.mu.Unlock()

	out := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		out = append(out, all[(start+i)%len(all)])
	}
	return out
}

func (c *RADOSGWCollector) observeRequest(op string, started time.Time, err error) {
	c.obsMu.Lock()
	defer c.obsMu.Unlock()

	c.lastRequestDur[op] = time.Since(started).Seconds()
	if err == nil {
		c.lastSuccessUnix[op] = float64(time.Now().Unix())
		return
	}

	reason := classifyErrorReason(err)
	c.requestErrors[requestErrorKey{op: op, reason: reason}]++
	if reason == "slowdown" {
		c.slowdownTotal[op]++
	}
}

func classifyErrorReason(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case isSlowDownError(err):
		return "slowdown"
	default:
		return "other"
	}
}

func (c *RADOSGWCollector) collectInternalTelemetry(ch chan<- prometheus.Metric) {
	c.obsMu.RLock()
	lastDur := make(map[string]float64, len(c.lastRequestDur))
	for op, v := range c.lastRequestDur {
		lastDur[op] = v
	}
	errs := make(map[requestErrorKey]uint64, len(c.requestErrors))
	for k, v := range c.requestErrors {
		errs[k] = v
	}
	slowdowns := make(map[string]uint64, len(c.slowdownTotal))
	for op, v := range c.slowdownTotal {
		slowdowns[op] = v
	}
	lastOK := make(map[string]float64, len(c.lastSuccessUnix))
	for op, v := range c.lastSuccessUnix {
		lastOK[op] = v
	}
	c.obsMu.RUnlock()

	for op, dur := range lastDur {
		ch <- prometheus.MustNewConstMetric(c.requestDuration, prometheus.GaugeValue, dur, op)
	}
	for k, total := range errs {
		ch <- prometheus.MustNewConstMetric(c.requestErrorsTotal, prometheus.CounterValue, float64(total), k.op, k.reason)
	}
	for op, total := range slowdowns {
		ch <- prometheus.MustNewConstMetric(c.requestSlowdown, prometheus.CounterValue, float64(total), op)
	}
	for op, ts := range lastOK {
		ch <- prometheus.MustNewConstMetric(c.requestLastSuccess, prometheus.GaugeValue, ts, op)
	}
}
