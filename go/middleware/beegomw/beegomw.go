// Package beegomw 提供 beego v2 框架的观测中间件（obs-sdk-go）。
//
// 与 net/http 的 middleware、gin 的 ginmw 职责一致（注入 request_id / community、
// 可选记录 http_server_* 服务器指标 —— 不加前缀，同一条 series 已带 service label，
// 见 spec/metrics-format.md），只是适配 beego 的过滤器链。beego 用户这样接入：
//
//	import "github.com/opensourceways/obs-sdk/go/middleware/beegomw"
//
//	m := obsmetrics.New(obsmetrics.Config{Service: "cla"})
//	beego.InsertFilterChain("/*", beegomw.Middleware(beegomw.Options{Metrics: m}))
//
// # 为什么必须用 InsertFilterChain，而不是 InsertFilter
//
// beego 的 InsertFilter 只能注册「单点」过滤器（BeforeRouter / AfterExec / …），
// 拿不到「请求开始 → 响应结束」的包裹语义；而 Controller.StopRun() 内部是
// panic(ErrAbort)，由 serveHttp 自己的 RecoverFunc 就地吃掉后直接返回，**不会**
// 回到 Admin 标签 —— 也就是说 AfterExec / FinishRouter 过滤器会被整个跳过。
// 在 beego 里 StopRun 常被用在鉴权失败路径上，用 AfterExec 记指标会静默漏掉
// 全部 401/403，失败率偏低且不报错。
//
// InsertFilterChain 注册的过滤器包住的正是 serveHttp 本身（beego 内部
// res.chainRoot = newFilterRouter("/*", res.serveHttp)），所以用它做的中间件
// 能保证每个请求恰好收尾一次，StopRun 也不例外。
package beegomw

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"time"

	beego "github.com/beego/beego/v2/server/web"
	beecontext "github.com/beego/beego/v2/server/web/context"

	"github.com/opensourceways/obs-sdk/go/metrics"
	"github.com/opensourceways/obs-sdk/go/sdkctx"
)

// HeaderRequestID 与 net/http 中间件、ginmw 保持一致。
const HeaderRequestID = "X-Request-Id"

// routerPatternKey 是 beego 路由命中后写入 Input 的路由模板 key，
// 与 server/web/router.go 中 ctx.Input.SetData("RouterPattern", …) 同名。
const routerPatternKey = "RouterPattern"

// unmatchedPath 是路由未命中（404 / 静态资源 / 未走路由）时的 path label 取值。
//
// 这里刻意**不回退到原始 URL**：未命中时路径由外部输入决定（扫描流量、伪造路径），
// 直接入 label 会撑爆时序基数（spec/metrics-format.md 的 label 约定）。
// 收敛成一个常量既保证基数有界，也如实表达「这条请求没有命中任何路由」。
const unmatchedPath = "unmatched"

// CommunityResolver 从 beego context 解析 community 覆盖值；空串表示不覆盖。
// 解析须来自可信判定点（路由前缀 / 认证主体 / 白名单），禁止裸读 URL 或 Header。
type CommunityResolver func(ctx *beecontext.Context) string

// Options 中间件配置。
type Options struct {
	// Metrics 若非 nil，则记录服务器指标。
	Metrics *metrics.Metrics
	// ResolveCommunity 可选：从请求解析 community。
	ResolveCommunity CommunityResolver
	// DisableRequestID 置 true 跳过 request_id 注入。
	DisableRequestID bool
}

// Middleware 返回 beego.FilterChain，交给 beego.InsertFilterChain 注册。
//
// 注意：metric 的注册发生在 Middleware 调用时，因此**每个 registry 只能调用一次**
// （同一进程内重复调用会因重复注册同名指标而 panic）。这与 ginmw / net/http 中间件
// 的约束一致。
func Middleware(opts Options) beego.FilterChain {
	var (
		reqTotal    *metrics.CounterVec
		reqDuration *metrics.HistogramVec
	)
	if m := opts.Metrics; m != nil {
		// 服务器公共指标不加任何前缀（spec：同一条 series 已带 service label，按 label 过滤）。
		reqTotal = m.NewCounterVec("http_server_requests_total", "HTTP requests handled",
			"method", "path", "status_code")
		reqDuration = m.NewHistogramVec("http_server_request_duration_seconds",
			"HTTP request latency", "method", "path", "status_code")
	}

	return func(next beego.FilterFunc) beego.FilterFunc {
		return func(ctx *beecontext.Context) {
			injectContext(ctx, opts)

			start := time.Now()
			defer func() {
				if r := recover(); r != nil {
					// beego 默认 RecoverPanic=true，panic 已在 serveHttp 内被记成 5xx；
					// 走到这里说明框架未接管该 panic。如实记一条 5xx 后原样抛出，
					// 不改变框架语义。
					record(reqTotal, reqDuration, ctx, start, 500)
					panic(r)
				}
				record(reqTotal, reqDuration, ctx, start, responseStatus(ctx))
			}()

			next(ctx)
		}
	}
}

// injectContext 把 request_id / community 写进 ctx.Request 的 context，
// 供框架内业务日志（obslog.InfoContext）与指标打点取用。
func injectContext(ctx *beecontext.Context, opts Options) {
	if ctx == nil || ctx.Request == nil {
		return
	}

	inner := ctx.Request.Context()

	if !opts.DisableRequestID {
		requestID := ctx.Input.Header(HeaderRequestID)
		if requestID == "" {
			requestID = newRequestID()
		}
		inner = sdkctx.WithRequestID(inner, requestID)
	}

	if opts.ResolveCommunity != nil {
		if community := opts.ResolveCommunity(ctx); community != "" {
			inner = sdkctx.WithCommunity(inner, community)
		}
	}

	ctx.Request = ctx.Request.WithContext(inner)
}

// responseStatus 取响应状态码。未显式写过状态时 beego 自己按 200 记录
// （server/web/router.go 的 Admin 段：statusCode == 0 → 200），此处对齐同一口径。
func responseStatus(ctx *beecontext.Context) int {
	if ctx == nil || ctx.ResponseWriter == nil || ctx.ResponseWriter.Status == 0 {
		return 200
	}
	return ctx.ResponseWriter.Status
}

// pathLabel 取路由模板而非原始 URL，保证 label 基数有界。
func pathLabel(ctx *beecontext.Context) string {
	if ctx == nil {
		return unmatchedPath
	}
	if pattern, ok := ctx.Input.GetData(routerPatternKey).(string); ok && pattern != "" {
		return pattern
	}
	return unmatchedPath
}

// record 记一条服务器指标。metric 未配置（Metrics 为 nil）时为空操作。
//
// path 用路由模板、statusCode 用显式入参（而非此处再读一次 ctx），
// 是因为 panic 分支下 ctx 的状态码未必可信。
func record(
	reqTotal *metrics.CounterVec,
	reqDuration *metrics.HistogramVec,
	ctx *beecontext.Context,
	start time.Time,
	statusCode int,
) {
	if reqTotal == nil || ctx == nil || ctx.Request == nil {
		return
	}

	inner := ctx.Request.Context()
	path := pathLabel(ctx)
	status := strconv.Itoa(statusCode)
	duration := time.Since(start).Seconds()

	reqTotal.AddWithContext(inner, 1, ctx.Request.Method, path, status)
	reqDuration.ObserveWithContext(inner, duration, ctx.Request.Method, path, status)
}

func newRequestID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b)
}
