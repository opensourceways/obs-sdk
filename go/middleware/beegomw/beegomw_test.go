package beegomw

import (
	"net/http"
	"net/http/httptest"
	"testing"

	beego "github.com/beego/beego/v2/server/web"
	beecontext "github.com/beego/beego/v2/server/web/context"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opensourceways/obs-sdk/go/metrics"
	"github.com/opensourceways/obs-sdk/go/sdkctx"
)

func noProcess() *bool { b := false; return &b }

// newMetrics 造一个独立 registry，避免用例间指标重复注册。
func newMetrics() *metrics.Metrics {
	return metrics.New(metrics.Config{
		Service:        "cla",
		Env:            "test",
		Instance:       "pod-1",
		Community:      "openeuler",
		IncludeProcess: noProcess(),
	})
}

// newRegister 造一个独立的 ControllerRegister 并挂上中间件。
// 用独立注册器而非全局 BeeApp：避免用例之间共享路由与过滤器链，也便于并发跑。
func newRegister(m *metrics.Metrics, opts Options) *beego.ControllerRegister {
	if opts.Metrics == nil {
		opts.Metrics = m
	}
	reg := beego.NewControllerRegister()
	reg.InsertFilterChain("/*", Middleware(opts))
	return reg
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

// gatherLabels 收集该 family 下所有 series 的 path label，用于基数断言。
func gatherLabels(t *testing.T, m *metrics.Metrics, familyName, labelName string) []string {
	t.Helper()
	fams, err := m.Registry().Gather()
	require.NoError(t, err)
	var out []string
	for _, f := range fams {
		if f.GetName() != familyName {
			continue
		}
		for _, s := range f.GetMetric() {
			for _, lp := range s.GetLabel() {
				if lp.GetName() == labelName {
					out = append(out, lp.GetValue())
				}
			}
		}
	}
	return out
}

func TestRequestIDInjected(t *testing.T) {
	var got string
	m := newMetrics()
	reg := newRegister(m, Options{})
	reg.AddMethod("GET", "/ping", func(ctx *beecontext.Context) {
		got = sdkctx.RequestID(ctx.Request.Context())
		ctx.ResponseWriter.WriteHeader(http.StatusOK)
	})
	reg.Init()

	w := httptest.NewRecorder()
	reg.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ping", nil))

	require.Equal(t, http.StatusOK, w.Code)
	assert.NotEmpty(t, got)
}

func TestRequestIDHonorsInbound(t *testing.T) {
	var got string
	m := newMetrics()
	reg := newRegister(m, Options{})
	reg.AddMethod("GET", "/ping", func(ctx *beecontext.Context) {
		got = sdkctx.RequestID(ctx.Request.Context())
		ctx.ResponseWriter.WriteHeader(http.StatusOK)
	})
	reg.Init()

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set(HeaderRequestID, "inbound-1")

	w := httptest.NewRecorder()
	reg.ServeHTTP(w, req)

	assert.Equal(t, "inbound-1", got)
}

func TestCommunityResolverWritesContext(t *testing.T) {
	var got string
	m := newMetrics()
	reg := newRegister(m, Options{
		ResolveCommunity: func(ctx *beecontext.Context) string {
			// 仅演示：真实场景须来自可信判定点（路由前缀 / 认证主体 / 白名单）。
			return "mindspore"
		},
	})
	reg.AddMethod("GET", "/ping", func(ctx *beecontext.Context) {
		got = sdkctx.Community(ctx.Request.Context())
		ctx.ResponseWriter.WriteHeader(http.StatusOK)
	})
	reg.Init()

	w := httptest.NewRecorder()
	reg.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ping", nil))

	assert.Equal(t, "mindspore", got)
	// community 覆盖发生在打点之前，指标上应体现覆盖值而非部署默认值。
	assert.Equal(t, float64(1), findCounter(t, m, "http_server_requests_total", map[string]string{
		"community": "mindspore", "method": "GET", "path": "/ping", "status_code": "200",
	}))
}

// TestPathLabelUsesRouterPattern 是 beegomw 相对 ginmw / net/http 中间件的核心差异：
// path label 取 beego 的路由模板，而不是带真实 ID 的原始 URL。
func TestPathLabelUsesRouterPattern(t *testing.T) {
	m := newMetrics()
	reg := newRegister(m, Options{})
	reg.AddMethod("GET", "/v1/cla/:link_id", func(ctx *beecontext.Context) {
		ctx.ResponseWriter.WriteHeader(http.StatusOK)
	})
	reg.Init()

	w := httptest.NewRecorder()
	reg.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/v1/cla/a1b2c3d4", nil))

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, float64(1), findCounter(t, m, "http_server_requests_total", map[string]string{
		"method": "GET", "path": "/v1/cla/:link_id", "status_code": "200",
	}))
	// 原始 URL 不得出现在任何 series 上。
	assert.NotContains(t, gatherLabels(t, m, "http_server_requests_total", "path"), "/v1/cla/a1b2c3d4")
}

// TestUnmatchedRouteLabelIsBounded 保证未命中路由时也不会把外部输入的路径写进 label：
// 打同一路由模板下的两个不同随机路径，应只产生一条 unmatched series。
func TestUnmatchedRouteLabelIsBounded(t *testing.T) {
	m := newMetrics()
	reg := newRegister(m, Options{})
	reg.AddMethod("GET", "/ping", func(ctx *beecontext.Context) {
		ctx.ResponseWriter.WriteHeader(http.StatusOK)
	})
	reg.Init()

	for _, p := range []string{"/nope/11111111", "/nope/22222222"} {
		w := httptest.NewRecorder()
		reg.ServeHTTP(w, httptest.NewRequest(http.MethodGet, p, nil))
	}

	assert.Equal(t, float64(2), findCounter(t, m, "http_server_requests_total", map[string]string{
		"method": "GET", "path": unmatchedPath,
	}))
	paths := gatherLabels(t, m, "http_server_requests_total", "path")
	assert.NotContains(t, paths, "/nope/11111111")
	assert.NotContains(t, paths, "/nope/22222222")
}

// stopRunController 复刻 app-cla-server 的鉴权失败路径：
// 先写响应，再 StopRun()。StopRun 内部是 panic(ErrAbort)，
// 由 serveHttp 自己的 RecoverFunc 就地吞掉，AfterExec / FinishRouter 过滤器会被跳过。
type stopRunController struct {
	beego.Controller
}

func (c *stopRunController) Post() {
	c.Ctx.ResponseWriter.WriteHeader(http.StatusUnauthorized)
	c.StopRun()
}

// TestStopRunStillRecorded 是改用 InsertFilterChain 的理由所在：
// 用 InsertFilter(AfterExec) 记指标时这条请求会被整个漏掉，失败率静默偏低。
func TestStopRunStillRecorded(t *testing.T) {
	m := newMetrics()
	reg := newRegister(m, Options{})
	reg.Add("/v1/link/:link_id", &stopRunController{})
	reg.Init()

	w := httptest.NewRecorder()
	reg.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1/link/abc123", nil))

	require.Equal(t, http.StatusUnauthorized, w.Code)
	assert.Equal(t, float64(1), findCounter(t, m, "http_server_requests_total", map[string]string{
		"method": "POST", "path": "/v1/link/:link_id", "status_code": "401",
	}))
}

// TestStatusRecorded 覆盖非 2xx 的正常返回路径与状态码口径。
func TestStatusRecorded(t *testing.T) {
	m := newMetrics()
	reg := newRegister(m, Options{})
	reg.AddMethod("POST", "/jobs", func(ctx *beecontext.Context) {
		ctx.ResponseWriter.WriteHeader(http.StatusCreated)
	})
	reg.Init()

	w := httptest.NewRecorder()
	reg.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/jobs", nil))

	require.Equal(t, http.StatusCreated, w.Code)
	assert.Equal(t, float64(1), findCounter(t, m, "http_server_requests_total", map[string]string{
		"method": "POST", "path": "/jobs", "status_code": "201",
	}))
}

// TestNilMetricsOnlyInjects 保证不配 Metrics 时只做上下文注入、不注册任何指标。
func TestNilMetricsOnlyInjects(t *testing.T) {
	var got string
	reg := beego.NewControllerRegister()
	reg.InsertFilterChain("/*", Middleware(Options{}))
	reg.AddMethod("GET", "/ping", func(ctx *beecontext.Context) {
		got = sdkctx.RequestID(ctx.Request.Context())
		ctx.ResponseWriter.WriteHeader(http.StatusOK)
	})
	reg.Init()

	w := httptest.NewRecorder()
	reg.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/ping", nil))

	require.Equal(t, http.StatusOK, w.Code)
	assert.NotEmpty(t, got)
}
