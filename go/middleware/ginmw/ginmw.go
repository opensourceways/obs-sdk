// Package ginmw 提供 gin 框架的观测中间件（obs-sdk-go）。
//
// 与 net/http 的 middleware 职责一致（注入 request_id / community、可选记录
// obs_http_server_* 服务器指标），只是适配 gin.HandlerFunc。gin 用户按此接入：
//
//	import "github.com/opensourceways/obs-sdk/go/middleware/ginmw"
//
//	m := metrics.New(metrics.Config{Service: "review"})
//	r := gin.New()
//	r.Use(ginmw.Middleware(ginmw.Options{Metrics: m}))
package ginmw

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/opensourceways/obs-sdk/go/metrics"
	"github.com/opensourceways/obs-sdk/go/sdkctx"
)

// HeaderRequestID 与 net/http 中间件一致。
const HeaderRequestID = "X-Request-Id"

// CommunityResolver 从 *gin.Context 解析 community 覆盖值；空串表示不覆盖。
// 解析须来自可信判定点（路由/认证主体/白名单）。
type CommunityResolver func(c *gin.Context) string

// Options 中间件配置。
type Options struct {
	// Metrics 若非 nil，则记录服务器指标。
	Metrics *metrics.Metrics
	// ResolveCommunity 可选：从请求解析 community。
	ResolveCommunity CommunityResolver
	// DisableRequestID 置 true 跳过 request_id 注入。
	DisableRequestID bool
}

// Middleware 返回 gin.HandlerFunc。
func Middleware(opts Options) gin.HandlerFunc {
	var (
		reqTotal    *metrics.CounterVec
		reqDuration *metrics.HistogramVec
	)
	if m := opts.Metrics; m != nil {
		reqTotal = m.NewCounterVec("http_server_requests_total", "HTTP requests handled",
			"method", "path", "status_code")
		reqDuration = m.NewHistogramVec("http_server_request_duration_seconds",
			"HTTP request latency", "method", "path", "status_code")
	}

	return func(c *gin.Context) {
		ctx := c.Request.Context()

		if !opts.DisableRequestID {
			rid := c.GetHeader(HeaderRequestID)
			if rid == "" {
				rid = newRequestID()
			}
			ctx = sdkctx.WithRequestID(ctx, rid)
		}
		if opts.ResolveCommunity != nil {
			if cc := opts.ResolveCommunity(c); cc != "" {
				ctx = sdkctx.WithCommunity(ctx, cc)
			}
		}
		c.Request = c.Request.WithContext(ctx)

		start := time.Now()
		c.Next()

		if reqTotal != nil {
			path := c.Request.URL.Path // 低基数提示：接入服务应归一化为路由模板
			reqTotal.AddWithContext(c.Request.Context(), 1,
				c.Request.Method, path, strconv.Itoa(c.Writer.Status()))
			reqDuration.ObserveWithContext(c.Request.Context(),
				time.Since(start).Seconds(), c.Request.Method, path,
				strconv.Itoa(c.Writer.Status()))
		}
	}
}

func newRequestID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b)
}
