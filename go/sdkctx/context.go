// Package sdkctx 定义请求级通用字段（community / request_id / trace_id / span_id）
// 在 context.Context 中的读写。log 与 metrics 两个 SDK package 共用本包，保证
// 双层注入（部署级默认 + 请求级覆盖）取自同一来源。
//
// 设计约束（见 spec/common-fields.md）：请求级 community 必须由业务在可信判定点
// （路由前缀 / 认证主体 / 白名单）显式写入，SDK 不负责从外部请求原样透传。
package sdkctx

import "context"

// key 是 context 值的私有 key 类型，避免与其他包冲突。
type key struct{}

// Request 请求级通用字段。
type Request struct {
	// Community 覆盖值；空串表示「未覆盖，回退部署级默认」。
	Community string
	// RequestID 单请求关联 ID；空串表示未设置。
	RequestID string
	// TraceID 预留位（二期 trace 接入）；本期恒为空。
	TraceID string
	// SpanID 预留位（二期 trace 接入）；本期恒为空。
	SpanID string
}

// WithCommunity 返回一个携带 community 覆盖值的新 context。
// 业务须在可信判定点调用（见包注释）。
func WithCommunity(ctx context.Context, community string) context.Context {
	return withField(ctx, func(r *Request) { r.Community = community })
}

// WithRequestID 返回一个携带 request_id 的新 context。
func WithRequestID(ctx context.Context, requestID string) context.Context {
	return withField(ctx, func(r *Request) { r.RequestID = requestID })
}

// WithTraceID 返回一个携带 trace_id 的新 context（二期 trace 预留注入点）。
func WithTraceID(ctx context.Context, traceID string) context.Context {
	return withField(ctx, func(r *Request) { r.TraceID = traceID })
}

// WithSpanID 返回一个携带 span_id 的新 context（二期 trace 预留注入点）。
func WithSpanID(ctx context.Context, spanID string) context.Context {
	return withField(ctx, func(r *Request) { r.SpanID = spanID })
}

// From 取出 context 中携带的请求级字段；未设置时返回零值 Request。
func From(ctx context.Context) Request {
	if ctx == nil {
		return Request{}
	}
	if v, ok := ctx.Value(key{}).(*Request); ok && v != nil {
		return *v
	}
	return Request{}
}

// Community 返回当前 community 覆盖值；未覆盖返回空串。
func Community(ctx context.Context) string {
	return From(ctx).Community
}

// RequestID 返回当前 request_id；未设置返回空串。
func RequestID(ctx context.Context) string {
	return From(ctx).RequestID
}

// TraceID 返回当前 trace_id；未设置返回空串。
func TraceID(ctx context.Context) string {
	return From(ctx).TraceID
}

// SpanID 返回当前 span_id；未设置返回空串。
func SpanID(ctx context.Context) string {
	return From(ctx).SpanID
}

func withField(ctx context.Context, mutate func(*Request)) context.Context {
	cur := From(ctx)
	mutate(&cur)
	return context.WithValue(ctx, key{}, &cur)
}
