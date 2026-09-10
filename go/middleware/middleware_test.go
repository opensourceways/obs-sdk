package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opensourceways/obs-sdk/go/metrics"
	"github.com/opensourceways/obs-sdk/go/sdkctx"
)

func noProcess() *bool { b := false; return &b }

func TestRequestIDInjected(t *testing.T) {
	var got string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = sdkctx.RequestID(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})

	mid := New(Options{})
	srv := httptest.NewServer(mid.Then(inner))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/ping")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)
	assert.NotEmpty(t, got)
}

func TestRequestIDHonorsInbound(t *testing.T) {
	var got string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = sdkctx.RequestID(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	mid := New(Options{})
	srv := httptest.NewServer(mid.Then(inner))
	defer srv.Close()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/ping", nil)
	req.Header.Set(HeaderRequestID, "inbound-1")
	_, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	assert.Equal(t, "inbound-1", got)
}

func TestCommunityResolverWritesContext(t *testing.T) {
	var got string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = sdkctx.Community(r.Context())
		w.WriteHeader(http.StatusOK)
	})

	mid := New(Options{
		ResolveCommunity: func(r *http.Request) string {
			// 仅演示：真实场景须来自可信判定点（路由/认证主体/白名单）。
			return "openeuler"
		},
	})
	srv := httptest.NewServer(mid.Then(inner))
	defer srv.Close()

	_, err := http.Get(srv.URL + "/ping")
	require.NoError(t, err)
	assert.Equal(t, "openeuler", got)
}

// findCounter 从 Gather 结果中按 family 名与 label 匹配找 counter 值。
func findCounter(t *testing.T, m *metrics.Metrics, familyName string, wantLabels map[string]string) float64 {
	t.Helper()
	fams, err := m.Registry().Gather()
	require.NoError(t, err)
	for _, f := range fams {
		if f.GetName() != familyName {
			continue
		}
		for _, s := range f.GetMetric() {
			labels := map[string]string{}
			for _, lp := range s.GetLabel() {
				labels[lp.GetName()] = lp.GetValue()
			}
			ok := true
			for k, v := range wantLabels {
				if labels[k] != v {
					ok = false
					break
				}
			}
			if ok {
				return s.GetCounter().GetValue()
			}
		}
	}
	t.Fatalf("family %s with labels %v not found", familyName, wantLabels)
	return 0
}

func TestHTTPMetricsRecordedWithDefaultCommunity(t *testing.T) {
	m := metrics.New(metrics.Config{
		Service:        "review",
		Env:            "test",
		Instance:       "pod-1",
		Community:      "openeuler",
		IncludeProcess: noProcess(),
	})
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})

	mid := New(Options{Metrics: m})
	srv := httptest.NewServer(mid.Then(inner))
	defer srv.Close()

	// POST /jobs → 201。
	_, err := http.Post(srv.URL+"/jobs", "text/plain", nil)
	require.NoError(t, err)

	// 服务器指标按默认 community 记（无覆盖）。
	assert.Equal(t, float64(1), findCounter(t, m,
		"http_server_requests_total", map[string]string{
			"service": "review", "env": "test", "instance": "pod-1",
			"community": "openeuler", "method": "POST",
			"path": "/jobs", "status_code": "201",
		}))
}

func TestHTTPMetricsRespectsCommunityOverride(t *testing.T) {
	m := metrics.New(metrics.Config{
		Service:        "review",
		Community:      "openeuler",
		IncludeProcess: noProcess(),
	})
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// 通过 ResolveCommunity 在中间件内完成注入，覆盖发生在打点（收尾）之前。
	mid := New(Options{
		Metrics: m,
		ResolveCommunity: func(r *http.Request) string {
			return "mindspore"
		},
	})
	srv := httptest.NewServer(mid.Then(inner))
	defer srv.Close()

	_, err := http.Get(srv.URL + "/jobs")
	require.NoError(t, err)

	assert.Equal(t, float64(1), findCounter(t, m,
		"http_server_requests_total", map[string]string{
			"community": "mindspore", "method": "GET",
			"path": "/jobs", "status_code": "200",
		}))
}
