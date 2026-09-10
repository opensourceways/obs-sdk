package sdkctx

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCommunityOverrideAndFallback(t *testing.T) {
	ctx := context.Background()

	// 未覆盖：Community 返回空串。
	assert.Equal(t, "", Community(ctx))

	// 设置覆盖后可读回。
	ctx = WithCommunity(ctx, "openeuler")
	assert.Equal(t, "openeuler", Community(ctx))

	// 覆盖是叠加而非替换：保留其他字段。
	ctx = WithRequestID(ctx, "req-1")
	r := From(ctx)
	assert.Equal(t, "openeuler", r.Community)
	assert.Equal(t, "req-1", r.RequestID)
}

func TestFromZeroValue(t *testing.T) {
	var r = From(nil)
	assert.Equal(t, Request{}, r)
	assert.Equal(t, "", r.Community)
}

func TestTraceIDReserved(t *testing.T) {
	// trace_id 预留注入位：本期可空，二期经 WithTraceID 注入即可见。
	ctx := WithTraceID(context.Background(), "trace-abc")
	assert.Equal(t, "trace-abc", TraceID(ctx))
	assert.Equal(t, "", RequestID(ctx))
}

func TestSpanIDReserved(t *testing.T) {
	// span_id 预留注入位：与 trace_id 同为二期预留，且互不干扰。
	ctx := WithTraceID(context.Background(), "trace-abc")
	ctx = WithSpanID(ctx, "span-xyz")
	assert.Equal(t, "span-xyz", SpanID(ctx))
	assert.Equal(t, "trace-abc", TraceID(ctx))
	assert.Equal(t, "", SpanID(context.Background()))
}
