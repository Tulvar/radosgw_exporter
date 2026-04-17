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
		enableUserStats,
		enableBucketStats,
		enableUsageMetrics,
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
		true,
		true,
		false,
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
