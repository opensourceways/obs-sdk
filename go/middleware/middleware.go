// Package middleware 提供 net/http 的观测中间件（obs-sdk-go）。
//
// 中间件职责（薄装配，不自研 instrumentation）：
//   - 为请求注入 request_id（无则生成）；有可信入站头（X-Request-Id）则沿用；
//   - 可选从可信来源解析 community 写入 context（由解析函数提供，SDK 不裸透传）；
//   - 若配置绑定了 *metrics.Metrics，则记录 HTTP 服务器指标
//     obs_http_server_requests_total / obs_http_server_request_duration_seconds。
//
// 用法（net/http）：
//
//	m := metrics.New(metrics.Config{Service: "review"})
//	mid := middleware.New(middleware.Options{Metrics: m})
//	http.ListenAndServe(":8080", mid.Then(http.DefaultServeMux))
package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strconv"
	"time"

	"github.com/opensourceways/obs-sdk/go/metrics"
	"github.com/opensourceways/obs-sdk/go/sdkctx"
)

// HeaderRequestID 是入站请求 ID 头名。中间件仅在请求已携带时沿用，
// 否则自动生成 —— 该头是否可信由部署方决定。
const HeaderRequestID = "X-Request-Id"

// CommunityResolver 从请求解析 community 覆盖值；返回空串表示「不覆盖，回退默认」。
// 解析必须来自可信判定点（路由/认证主体/白名单），不得原样透传外部入参。
type CommunityResolver func(r *http.Request) string

// Options 中间件配置。
type Options struct {
	// Metrics 若非 nil，则记录 HTTP 服务器指标。
	Metrics *metrics.Metrics
	// ResolveCommunity 可选：从请求解析 community。
	ResolveCommunity CommunityResolver
	// DisableRequestID 置 true 跳过 request_id 注入。
	DisableRequestID bool
}

// Middleware 是可复用的 net/http 中间件（Next 由 Then 注入）。
type Middleware struct {
	opts Options

	reqTotal    *metrics.CounterVec
	reqDuration *metrics.HistogramVec
}

// New 构建中间件。opts.Metrics 为 nil 时仅做上下文注入，不记指标。
func New(opts Options) *Middleware {
	md := &Middleware{opts: opts}
	if m := opts.Metrics; m != nil {
		// 服务器公共指标用 obs_ 前缀（spec：不以 service 名开头，按 label 过滤）。
		md.reqTotal = m.NewCounterVec("http_server_requests_total", "HTTP requests handled",
			"method", "path", "status_code")
		md.reqDuration = m.NewHistogramVec("http_server_request_duration_seconds",
			"HTTP request latency", "method", "path", "status_code")
	}
	return md
}

// Then 包装下游 handler。
func (md *Middleware) Then(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		if !md.opts.DisableRequestID {
			rid := r.Header.Get(HeaderRequestID)
			if rid == "" {
				rid = newRequestID()
			}
			ctx = sdkctx.WithRequestID(ctx, rid)
		}
		if md.opts.ResolveCommunity != nil {
			if c := md.opts.ResolveCommunity(r); c != "" {
				ctx = sdkctx.WithCommunity(ctx, c)
			}
		}
		r = r.WithContext(ctx)

		rec := &responseRecorder{ResponseWriter: w}
		start := time.Now()
		next.ServeHTTP(rec, r)
		dur := time.Since(start).Seconds()

		if md.reqTotal != nil {
			status := rec.status
			if status == 0 {
				status = http.StatusOK
			}
			// 低基数提示：path 可能含动态段，接入服务应将高基数路径归一化
			// 为路由模板再入 label（spec/metrics-format.md：路径可选 / 按模板入 label）。
			md.reqTotal.AddWithContext(ctx, 1, r.Method, r.URL.Path, strconv.Itoa(status))
			md.reqDuration.ObserveWithContext(ctx, dur, r.Method, r.URL.Path, strconv.Itoa(status))
		}
	})
}

type responseRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *responseRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *responseRecorder) Write(b []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

// Flush 透传 http.Flusher（SSE 等）。
func (r *responseRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func newRequestID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b)
}

// ContextWithCommunity 便捷函数：返回携带 community 覆盖值的 context
// （供业务在无中间件场景手动注入）。
func ContextWithCommunity(ctx context.Context, community string) context.Context {
	if community != "" {
		return sdkctx.WithCommunity(ctx, community)
	}
	return ctx
}
