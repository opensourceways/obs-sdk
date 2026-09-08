package log

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/opensourceways/obs-sdk/go/sdkctx"
)

// capture 构造写入 buffer 的 logger。
func capture(cfg Config) (*Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	cfg.Output = buf
	return New(cfg), buf
}

func parseLine(t *testing.T, line []byte) map[string]any {
	t.Helper()
	var m map[string]any
	assert.NoError(t, json.Unmarshal(line, &m))
	return m
}

func TestStaticFieldsInjected(t *testing.T) {
	l, buf := capture(Config{
		Service:   "review",
		Env:       "test",
		Instance:  "pod-1",
		Community: "openeuler",
	})

	l.Info("hello", "event", "pull_request")

	fields := parseLine(t, bytes.TrimSpace(buf.Bytes()))
	assert.Equal(t, "info", fields["level"])
	assert.Equal(t, "hello", fields["msg"])
	assert.Equal(t, "review", fields["service"])
	assert.Equal(t, "test", fields["env"])
	assert.Equal(t, "pod-1", fields["instance"])
	assert.Equal(t, "openeuler", fields["community"])
	assert.Equal(t, "pull_request", fields["event"])
	// 请求上下文缺省时 request_id / trace_id 不输出（键可缺省）。
	_, hasRid := fields["request_id"]
	_, hasTid := fields["trace_id"]
	assert.False(t, hasRid)
	assert.False(t, hasTid)
}

func TestRequestCommunityOverride(t *testing.T) {
	l, buf := capture(Config{
		Service:   "review",
		Community: "openeuler",
	})

	// 业务在可信判定点写入覆盖社区。
	ctx := sdkctx.WithCommunity(context.Background(), "mindspore")
	ctx = sdkctx.WithRequestID(ctx, "req-123")
	l.WithRequest(ctx).Info("scoped", "k", "v")

	fields := parseLine(t, bytes.TrimSpace(buf.Bytes()))
	assert.Equal(t, "mindspore", fields["community"])
	assert.Equal(t, "req-123", fields["request_id"])
}

func TestCommunityStaysDefaultWhenNoOverride(t *testing.T) {
	l, buf := capture(Config{Service: "review", Community: "ascend"})

	l.Info("no override")

	fields := parseLine(t, bytes.TrimSpace(buf.Bytes()))
	assert.Equal(t, "ascend", fields["community"])
}

func TestLevelFilter(t *testing.T) {
	l, buf := capture(Config{Service: "review", Level: "warn"})

	l.Info("dropped")
	l.Warn("kept")

	// debug/info 被过滤，只留 warn 一行。
	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	assert.Len(t, lines, 1)
	fields := parseLine(t, lines[0])
	assert.Equal(t, "warn", fields["level"])
	assert.Equal(t, "kept", fields["msg"])
}

func TestWithAddsFields(t *testing.T) {
	l, buf := capture(Config{Service: "review"})
	child := l.With("component", "webhook")
	child.Info("with fields", "extra", 1)

	fields := parseLine(t, bytes.TrimSpace(buf.Bytes()))
	assert.Equal(t, "webhook", fields["component"])
	assert.Equal(t, float64(1), fields["extra"])
}

func TestTraceIDReservedInject(t *testing.T) {
	l, buf := capture(Config{Service: "review", Community: "openeuler"})
	// 二期：trace 经 WithTraceID 注入，日志零返工带上 trace_id。
	ctx := sdkctx.WithTraceID(context.Background(), "trace-xyz")
	l.WithRequest(ctx).Info("with trace")

	fields := parseLine(t, bytes.TrimSpace(buf.Bytes()))
	assert.Equal(t, "trace-xyz", fields["trace_id"])
	assert.Equal(t, "openeuler", fields["community"])
}
