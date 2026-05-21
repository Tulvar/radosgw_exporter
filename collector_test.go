package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testWriter struct{ strings.Builder }

func (tw *testWriter) Write(p []byte) (int, error) { return tw.Builder.Write(p) }

type fakeHTTPClient struct {
	do func(req *http.Request) (*http.Response, error)
}

func (c fakeHTTPClient) Do(req *http.Request) (*http.Response, error) {
	return c.do(req)
}

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(&testWriter{}, nil))
}

func newTestCollector(t *testing.T, endpoint string, timeout time.Duration, enableUserStats, enableBucketStats, enableUsageMetrics bool) *RADOSGWCollector {
	t.Helper()

	c, err := NewRADOSGWCollector(
		endpoint,
		"access",
		"secret",
		false,
		timeout,
		0,
		0,
		enableUserStats,
		enableBucketStats,
		enableUsageMetrics,
		0,
		newTestLogger(),
	)
	require.NoError(t, err)
	require.NotNil(t, c)
	return c
}

func jsonResponse(t *testing.T, body any) *http.Response {
	t.Helper()

	data, err := json.Marshal(body)
	require.NoError(t, err)

	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(string(data))),
		Header:     make(http.Header),
	}
}

func collectMetricDescs(c *RADOSGWCollector) []string {
	ch := make(chan prometheus.Metric, 256)
	go func() {
		c.Collect(ch)
		close(ch)
	}()

	var descs []string
	for m := range ch {
		descs = append(descs, m.Desc().String())
	}
	return descs
}

func collectMetrics(c *RADOSGWCollector) []prometheus.Metric {
	ch := make(chan prometheus.Metric, 256)
	go func() {
		c.Collect(ch)
		close(ch)
	}()

	var out []prometheus.Metric
	for m := range ch {
		out = append(out, m)
	}
	return out
}

func gaugeValueFromMetrics(t *testing.T, metrics []prometheus.Metric, metricName string) (float64, bool) {
	t.Helper()
	for _, m := range metrics {
		if !strings.Contains(m.Desc().String(), metricName) {
			continue
		}
		pb := &dto.Metric{}
		require.NoError(t, m.Write(pb))
		if pb.Gauge != nil && pb.Gauge.Value != nil {
			return *pb.Gauge.Value, true
		}
	}
	return 0, false
}

func metricValueByLabels(t *testing.T, metrics []prometheus.Metric, metricName string, labels map[string]string) (float64, bool) {
	t.Helper()
	for _, m := range metrics {
		if !strings.Contains(m.Desc().String(), metricName) {
			continue
		}
		pb := &dto.Metric{}
		require.NoError(t, m.Write(pb))

		matches := true
		for k, want := range labels {
			found := false
			for _, l := range pb.Label {
				if l.GetName() == k && l.GetValue() == want {
					found = true
					break
				}
			}
			if !found {
				matches = false
				break
			}
		}
		if !matches {
			continue
		}

		if pb.Counter != nil && pb.Counter.Value != nil {
			return *pb.Counter.Value, true
		}
		if pb.Gauge != nil && pb.Gauge.Value != nil {
			return *pb.Gauge.Value, true
		}
	}
	return 0, false
}

func findGaugeValue(t *testing.T, descs <-chan prometheus.Metric, metricName string) *float64 {
	t.Helper()

	for m := range descs {
		if !strings.Contains(m.Desc().String(), metricName) {
			continue
		}

		pb := &dto.Metric{}
		require.NoError(t, m.Write(pb))
		if pb.Gauge != nil {
			return pb.Gauge.Value
		}
	}
	return nil
}

func TestNewRADOSGWCollector_CreatesSuccessfully(t *testing.T) {
	c, err := NewRADOSGWCollector(
		"http://localhost:8000",
		"a",
		"b",
		false,
		10*time.Second,
		0,
		0,
		true,
		true,
		false,
		0,
		newTestLogger(),
	)

	require.NoError(t, err)
	require.NotNil(t, c)
	require.NotNil(t, c.client)
}

func TestCollect_UsageTimeout_GracefulDegradation(t *testing.T) {
	c := newTestCollector(t, "http://example.com", 50*time.Millisecond, false, false, true)
	c.client.HTTPClient = fakeHTTPClient{
		do: func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, "/admin/usage") {
				return nil, context.DeadlineExceeded
			}
			return nil, errors.New("unexpected request")
		},
	}

	ch := make(chan prometheus.Metric, 32)
	go func() {
		c.Collect(ch)
		close(ch)
	}()

	upValue := findGaugeValue(t, ch, "radosgw_up")
	require.NotNil(t, upValue)
	assert.Equal(t, 1.0, *upValue)
}

func TestCollect_CriticalError_SetsUpToZero(t *testing.T) {
	c := newTestCollector(t, "http://example.com", 2*time.Second, true, true, true)
	c.client.HTTPClient = fakeHTTPClient{
		do: func(req *http.Request) (*http.Response, error) {
			return nil, errors.New("boom")
		},
	}

	ch := make(chan prometheus.Metric, 32)
	go func() {
		c.Collect(ch)
		close(ch)
	}()

	upValue := findGaugeValue(t, ch, "radosgw_up")
	require.NotNil(t, upValue)
	assert.Equal(t, 0.0, *upValue)
}

func TestCollect_BucketStatsOnly_EmitsBucketMetrics(t *testing.T) {
	c := newTestCollector(t, "http://example.com", 2*time.Second, false, true, false)
	c.client.HTTPClient = fakeHTTPClient{
		do: func(req *http.Request) (*http.Response, error) {
			switch {
			case strings.Contains(req.URL.Path, "/admin/metadata/user"):
				return jsonResponse(t, []string{"user1"}), nil
			case strings.Contains(req.URL.Path, "/admin/user"):
				return jsonResponse(t, map[string]any{
					"user_id": "user1",
				}), nil
			case strings.Contains(req.URL.Path, "/admin/bucket"):
				return jsonResponse(t, []map[string]any{
					{
						"bucket": "bucket-a",
						"owner":  "user1",
						"usage": map[string]any{
							"rgw.main": map[string]any{
								"num_objects": 3,
								"size_actual": 2048,
							},
							"rgw.multimeta": map[string]any{},
						},
						"bucket_quota": map[string]any{
							"enabled":     true,
							"max_size_kb": 4,
							"max_objects": 7,
						},
					},
				}), nil
			default:
				return nil, errors.New("unexpected request")
			}
		},
	}
	descs := collectMetricDescs(c)

	assert.Contains(t, strings.Join(descs, "\n"), "radosgw_bucket_usage_bytes")
	assert.Contains(t, strings.Join(descs, "\n"), "radosgw_bucket_usage_objects")
	assert.NotContains(t, strings.Join(descs, "\n"), "radosgw_user_total_bytes")
}

func TestCollect_UserStatsOnly_EmitsUserMetrics(t *testing.T) {
	c := newTestCollector(t, "http://example.com", 2*time.Second, true, false, false)
	c.client.HTTPClient = fakeHTTPClient{
		do: func(req *http.Request) (*http.Response, error) {
			switch {
			case strings.Contains(req.URL.Path, "/admin/metadata/user"):
				return jsonResponse(t, []string{"user1"}), nil
			case strings.Contains(req.URL.Path, "/admin/user"):
				return jsonResponse(t, map[string]any{
					"user_id": "user1",
					"stats": map[string]any{
						"size":        1024,
						"num_objects": 2,
					},
					"user_quota": map[string]any{
						"enabled":     true,
						"max_size_kb": 8,
						"max_objects": 5,
					},
					"bucket_quota": map[string]any{
						"enabled":     true,
						"max_size_kb": 4,
						"max_objects": 3,
					},
				}), nil
			default:
				return nil, errors.New("unexpected request")
			}
		},
	}

	descs := collectMetricDescs(c)
	all := strings.Join(descs, "\n")

	assert.Contains(t, all, "radosgw_user_total_bytes")
	assert.Contains(t, all, "radosgw_user_total_objects")
	assert.Contains(t, all, "radosgw_user_quota_enabled")
	assert.NotContains(t, all, "radosgw_bucket_usage_bytes")
}

func TestCollect_UserStats_DuplicateUserIDAcrossUIDs_AggregatesByUserLabel(t *testing.T) {
	c := newTestCollector(t, "http://example.com", 2*time.Second, true, false, false)
	c.client.HTTPClient = fakeHTTPClient{
		do: func(req *http.Request) (*http.Response, error) {
			switch {
			case strings.Contains(req.URL.Path, "/admin/metadata/user"):
				return jsonResponse(t, []string{"tenant-a$s3-user", "tenant-b$s3-user"}), nil
			case strings.Contains(req.URL.Path, "/admin/user"):
				uid := req.URL.Query().Get("uid")
				switch uid {
				case "tenant-a$s3-user":
					return jsonResponse(t, map[string]any{
						"user_id": "s3-user",
						"stats": map[string]any{
							"size":        1024,
							"num_objects": 2,
						},
					}), nil
				case "tenant-b$s3-user":
					return jsonResponse(t, map[string]any{
						"user_id": "s3-user",
						"stats": map[string]any{
							"size":        2048,
							"num_objects": 3,
						},
					}), nil
				default:
					return nil, errors.New("unexpected uid")
				}
			default:
				return nil, errors.New("unexpected request")
			}
		},
	}

	reg := prometheus.NewRegistry()
	require.NoError(t, reg.Register(c))
	_, err := reg.Gather()
	require.NoError(t, err)

	metrics := collectMetrics(c)
	size, ok := metricValueByLabels(t, metrics, "radosgw_user_total_bytes", map[string]string{
		"user": "s3-user",
	})
	require.True(t, ok)
	assert.Equal(t, 3072.0, size)
}

func TestCollect_GetUsersError_SetsUpToZero(t *testing.T) {
	c := newTestCollector(t, "http://example.com", 2*time.Second, true, true, false)
	c.client.HTTPClient = fakeHTTPClient{
		do: func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, "/admin/metadata/user") {
				return nil, errors.New("cannot list users")
			}
			return nil, errors.New("unexpected request")
		},
	}

	ch := make(chan prometheus.Metric, 32)
	go func() {
		c.Collect(ch)
		close(ch)
	}()

	upValue := findGaugeValue(t, ch, "radosgw_up")
	require.NotNil(t, upValue)
	assert.Equal(t, 0.0, *upValue)
}

func TestCollect_UsageDisabled_DoesNotCallUsageEndpoint(t *testing.T) {
	var usageCalls atomic.Int32

	c := newTestCollector(t, "http://example.com", 2*time.Second, false, true, false)
	c.client.HTTPClient = fakeHTTPClient{
		do: func(req *http.Request) (*http.Response, error) {
			switch {
			case strings.Contains(req.URL.Path, "/admin/usage"):
				usageCalls.Add(1)
				return nil, errors.New("usage should not be called")
			case strings.Contains(req.URL.Path, "/admin/metadata/user"):
				return jsonResponse(t, []string{"user1"}), nil
			case strings.Contains(req.URL.Path, "/admin/user"):
				return jsonResponse(t, map[string]any{
					"user_id": "user1",
				}), nil
			case strings.Contains(req.URL.Path, "/admin/bucket"):
				return jsonResponse(t, []map[string]any{}), nil
			default:
				return nil, errors.New("unexpected request")
			}
		},
	}
	_ = collectMetricDescs(c)

	assert.Equal(t, int32(0), usageCalls.Load())
}

func TestCollect_UsageTransientError_UsesStaleCache(t *testing.T) {
	var usageCalls atomic.Int32

	c := newTestCollector(t, "http://example.com", 2*time.Second, false, false, true)
	c.client.HTTPClient = fakeHTTPClient{
		do: func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, "/admin/usage") {
				call := usageCalls.Add(1)
				if call == 1 {
					return jsonResponse(t, map[string]any{
						"entries": []map[string]any{
							{
								"user": "u1",
								"buckets": []map[string]any{
									{
										"bucket": "b1",
										"owner":  "u1",
										"epoch":  10,
										"categories": []map[string]any{
											{
												"category":       "read",
												"ops":            5,
												"successful_ops": 5,
												"bytes_sent":     100,
												"bytes_received": 50,
											},
										},
									},
								},
							},
						},
					}), nil
				}
				return nil, context.DeadlineExceeded
			}
			return nil, errors.New("unexpected request")
		},
	}

	_ = collectMetrics(c) // warm cache
	second := collectMetrics(c)

	up, ok := gaugeValueFromMetrics(t, second, "radosgw_up")
	require.True(t, ok)
	assert.Equal(t, 1.0, up)

	stale, ok := gaugeValueFromMetrics(t, second, "radosgw_usage_cache_stale")
	require.True(t, ok)
	assert.Equal(t, 1.0, stale)

	age, ok := gaugeValueFromMetrics(t, second, "radosgw_usage_cache_age_seconds")
	require.True(t, ok)
	assert.GreaterOrEqual(t, age, 0.0)
}

func TestCollect_UsageNonTransientError_DoesNotUseStaleCache(t *testing.T) {
	var usageCalls atomic.Int32

	c := newTestCollector(t, "http://example.com", 2*time.Second, false, false, true)
	c.client.HTTPClient = fakeHTTPClient{
		do: func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, "/admin/usage") {
				call := usageCalls.Add(1)
				if call == 1 {
					return jsonResponse(t, map[string]any{
						"entries": []map[string]any{
							{
								"user": "u1",
								"buckets": []map[string]any{
									{
										"bucket": "b1",
										"owner":  "u1",
										"epoch":  10,
										"categories": []map[string]any{
											{
												"category":       "read",
												"ops":            5,
												"successful_ops": 5,
												"bytes_sent":     100,
												"bytes_received": 50,
											},
										},
									},
								},
							},
						},
					}), nil
				}
				return nil, errors.New("permission denied")
			}
			return nil, errors.New("unexpected request")
		},
	}

	_ = collectMetrics(c) // warm cache
	second := collectMetrics(c)

	up, ok := gaugeValueFromMetrics(t, second, "radosgw_up")
	require.True(t, ok)
	assert.Equal(t, 0.0, up)

	_, stalePresent := gaugeValueFromMetrics(t, second, "radosgw_usage_cache_stale")
	assert.False(t, stalePresent)
}

func TestCollect_UsageSlowDown_RetriesAndRecovers(t *testing.T) {
	var usageCalls atomic.Int32

	c := newTestCollector(t, "http://example.com", 2*time.Second, false, false, true)
	c.client.HTTPClient = fakeHTTPClient{
		do: func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, "/admin/usage") {
				call := usageCalls.Add(1)
				if call <= 2 {
					return nil, errors.New("SlowDown temporary")
				}
				return jsonResponse(t, map[string]any{
					"entries": []map[string]any{
						{
							"user": "u1",
							"buckets": []map[string]any{
								{
									"bucket": "b1",
									"owner":  "u1",
									"epoch":  10,
									"categories": []map[string]any{
										{
											"category":       "read",
											"ops":            5,
											"successful_ops": 5,
											"bytes_sent":     100,
											"bytes_received": 50,
										},
									},
								},
							},
						},
					},
				}), nil
			}
			return nil, errors.New("unexpected request")
		},
	}

	metrics := collectMetrics(c)

	assert.Equal(t, int32(3), usageCalls.Load())

	up, ok := gaugeValueFromMetrics(t, metrics, "radosgw_up")
	require.True(t, ok)
	assert.Equal(t, 1.0, up)

	slowdownErrors, ok := metricValueByLabels(t, metrics, "radosgw_exporter_request_errors_total", map[string]string{
		"op":     "usage",
		"reason": "slowdown",
	})
	require.True(t, ok)
	assert.Equal(t, 2.0, slowdownErrors)

	slowdownTotal, ok := metricValueByLabels(t, metrics, "radosgw_exporter_slowdown_total", map[string]string{
		"op": "usage",
	})
	require.True(t, ok)
	assert.Equal(t, 2.0, slowdownTotal)

	lastSuccess, ok := metricValueByLabels(t, metrics, "radosgw_exporter_last_success_unixtime", map[string]string{
		"op": "usage",
	})
	require.True(t, ok)
	assert.Greater(t, lastSuccess, 0.0)

	lastDuration, ok := metricValueByLabels(t, metrics, "radosgw_exporter_request_duration_seconds", map[string]string{
		"op": "usage",
	})
	require.True(t, ok)
	assert.GreaterOrEqual(t, lastDuration, 0.0)
}

func TestCollect_MaxUsersPerScrape_BatchesRoundRobin(t *testing.T) {
	c := newTestCollector(t, "http://example.com", 2*time.Second, true, false, false)
	c.maxUsersPerScrape = 1

	var calls []string
	c.client.HTTPClient = fakeHTTPClient{
		do: func(req *http.Request) (*http.Response, error) {
			switch {
			case strings.Contains(req.URL.Path, "/admin/metadata/user"):
				return jsonResponse(t, []string{"u1", "u2"}), nil
			case strings.Contains(req.URL.Path, "/admin/user"):
				uid := req.URL.Query().Get("uid")
				calls = append(calls, uid)
				return jsonResponse(t, map[string]any{
					"user_id": uid,
					"stats": map[string]any{
						"size":        1,
						"num_objects": 1,
					},
				}), nil
			default:
				return nil, errors.New("unexpected request")
			}
		},
	}

	_ = collectMetrics(c)
	_ = collectMetrics(c)
	_ = collectMetrics(c)

	require.GreaterOrEqual(t, len(calls), 3)
	assert.Equal(t, "u1", calls[0])
	assert.Equal(t, "u2", calls[1])
	assert.Equal(t, "u1", calls[2])
}
