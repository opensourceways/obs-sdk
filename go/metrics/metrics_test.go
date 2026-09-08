package metrics

import (
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opensourceways/obs-sdk/go/sdkctx"
)

func noProcess() *bool { b := false; return &b }

func newTest(t *testing.T, cfg Config) *Metrics {
	t.Helper()
	cfg.IncludeProcess = noProcess()
	return New(cfg)
}

func TestConstLabelsPresent(t *testing.T) {
	m := newTest(t, Config{Service: "review", Env: "test", Instance: "pod-1", Community: "openeuler"})
	c := m.NewCounterVec("events_total", "events")
	c.Inc()

	fam, err := m.Registry().Gather()
	require.NoError(t, err)
	require.Len(t, fam, 1)

	series := fam[0].GetMetric()
	require.Len(t, series, 1)

	seen := map[string]string{}
	for _, lp := range series[0].GetLabel() {
		seen[lp.GetName()] = lp.GetValue()
	}
	assert.Equal(t, "review", seen["service"])
	assert.Equal(t, "test", seen["env"])
	assert.Equal(t, "pod-1", seen["instance"])
	assert.Equal(t, "openeuler", seen["community"])
	assert.Equal(t, "events_total", fam[0].GetName())
}

func TestDefaultCommunityValue(t *testing.T) {
	m := newTest(t, Config{Service: "review", Community: "openeuler"})
	c := m.NewCounterVec("events_total", "events")
	c.Inc()

	// 打点用部署级默认 community。
	assert.Equal(t, float64(1), testutil.ToFloat64(
		c.Vec().WithLabelValues("openeuler")))
}

func TestDynamicCommunityOverride(t *testing.T) {
	m := newTest(t, Config{Service: "review", Community: "openeuler"})
	c := m.NewCounterVec("events_total", "events", "kind")

	c.Add(2, "pr")                          // 默认 openeuler
	c.IncWithContext(ctx("mindspore"), "pr") // 覆盖 mindspore

	assert.Equal(t, float64(2), testutil.ToFloat64(c.Vec().WithLabelValues("openeuler", "pr")))
	assert.Equal(t, float64(1), testutil.ToFloat64(c.Vec().WithLabelValues("mindspore", "pr")))

	// 覆盖只影响打点时刻，不污染后续默认值。
	c.Add(1, "issue")
	assert.Equal(t, float64(1), testutil.ToFloat64(c.Vec().WithLabelValues("openeuler", "issue")))
}

func TestGauge(t *testing.T) {
	m := newTest(t, Config{Service: "review", Community: "openeuler"})
	g := m.NewGaugeVec("in_flight", "in flight")
	g.Set(3)
	g.Inc()
	assert.Equal(t, float64(4), testutil.ToFloat64(g.Vec().WithLabelValues("openeuler")))
}

func TestHistogramCount(t *testing.T) {
	m := newTest(t, Config{Service: "review", Community: "openeuler"})
	h := m.NewHistogramVec("request_duration_seconds", "latency", "method")
	h.Observe(0.1, "GET")
	h.Observe(0.2, "GET")

	// histogram 不能 testutil.ToFloat64 直接读，改按 family 校验 _count。
	fam, err := m.Registry().Gather()
	require.NoError(t, err)
	require.Len(t, fam, 1)
	mf := fam[0].GetMetric()
	require.Len(t, mf, 1)
	assert.Equal(t, uint64(2), mf[0].GetHistogram().GetSampleCount())
	assert.Equal(t, "request_duration_seconds", fam[0].GetName())
}

func TestFQNameNamespace(t *testing.T) {
	m := newTest(t, Config{Service: "review", Namespace: "obs"})
	c := m.NewCounterVec("events_total", "events")
	c.Inc()

	fam, err := m.Registry().Gather()
	require.NoError(t, err)
	require.Equal(t, "obs_events_total", fam[0].GetName())
}

func ctx(community string) context.Context {
	return sdkctx.WithCommunity(context.Background(), community)
}
