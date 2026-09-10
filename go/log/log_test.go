package log

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opensourceways/obs-sdk/go/sdkctx"
)

// captureDefault 把包级 logger 指向 buffer；用例结束恢复原默认。
func captureDefault(t *testing.T, cfg Config) *bytes.Buffer {
	t.Helper()
	prev := slog.Default()
	t.Cleanup(func() { slog.SetDefault(prev) })

	buf := &bytes.Buffer{}
	cfg.Output = buf
	Init(cfg)
	return buf
}

// newLogger 返回独立 logger + buffer，不触碰全局默认（用于 slog.Logger 直用路径）。
func newLogger(t *testing.T, cfg Config) (*slog.Logger, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	cfg.Output = buf
	return New(cfg), buf
}

func parseLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	raw := strings.TrimSpace(buf.String())
	if raw == "" {
		return nil
	}
	lines := strings.Split(raw, "\n")
	out := make([]map[string]any, 0, len(lines))
	for _, l := range lines {
		var m map[string]any
		require.NoError(t, json.Unmarshal([]byte(l), &m), "非法 JSON 行：%s", l)
		out = append(out, m)
	}
	return out
}

func TestStaticFieldsInjected(t *testing.T) {
	buf := captureDefault(t, Config{
		Service:   "review",
		Env:       "test",
		Instance:  "pod-1",
		Community: "openeuler",
	})

	Info("hello", "event", "pull_request")

	fields := parseLines(t, buf)[0]
	assert.Equal(t, "info", fields["level"])
	assert.Equal(t, "hello", fields["msg"])
	assert.Equal(t, "review", fields["service"])
	assert.Equal(t, "test", fields["env"])
	assert.Equal(t, "pod-1", fields["instance"])
	assert.Equal(t, "openeuler", fields["community"])
	assert.Equal(t, "pull_request", fields["event"])
	// 请求上下文缺省时 request_id / trace_id / span_id 不输出（键可缺省）。
	for _, k := range []string{"request_id", "trace_id", "span_id"} {
		_, has := fields[k]
		assert.Falsef(t, has, "无上下文时不应输出 %s", k)
	}
}

// 核心能力：InfoContext 无需先绑定 logger，请求级字段自动从 ctx 附加。
func TestContextVariantInjectsRequestFields(t *testing.T) {
	buf := captureDefault(t, Config{Service: "review", Community: "openeuler"})

	ctx := sdkctx.WithCommunity(context.Background(), "mindspore")
	ctx = sdkctx.WithRequestID(ctx, "req-123")
	ctx = sdkctx.WithTraceID(ctx, "trace-xyz")
	ctx = sdkctx.WithSpanID(ctx, "span-abc")

	InfoContext(ctx, "scoped", "k", "v")

	fields := parseLines(t, buf)[0]
	assert.Equal(t, "mindspore", fields["community"]) // 请求级覆盖部署级默认
	assert.Equal(t, "req-123", fields["request_id"])
	assert.Equal(t, "trace-xyz", fields["trace_id"])
	assert.Equal(t, "span-abc", fields["span_id"])
	assert.Equal(t, "v", fields["k"])
}

func TestCommunityStaysDefaultWhenNoOverride(t *testing.T) {
	buf := captureDefault(t, Config{Service: "review", Community: "ascend"})

	InfoContext(context.Background(), "no override")

	assert.Equal(t, "ascend", parseLines(t, buf)[0]["community"])
}

// 二级 API：With(...) 附加字段，且能派生多个互不影响的子 logger。
func TestWithAddsFields(t *testing.T) {
	l, buf := newLogger(t, Config{Service: "review", DisableSource: true})

	child := l.With("component", "webhook")
	child.Info("with fields", "extra", 1)
	l.Info("parent untouched")

	lines := parseLines(t, buf)
	assert.Equal(t, "webhook", lines[0]["component"])
	assert.Equal(t, float64(1), lines[0]["extra"])
	_, has := lines[1]["component"]
	assert.False(t, has, "父 logger 不应被子 logger 的 With 影响")
}

func TestLevelFilter(t *testing.T) {
	buf := captureDefault(t, Config{Service: "review", Level: "warn"})

	Debug("dropped")
	Info("dropped")
	Warn("kept")
	Error("kept too")

	lines := parseLines(t, buf)
	require.Len(t, lines, 2)
	assert.Equal(t, "warn", lines[0]["level"])
	assert.Equal(t, "kept", lines[0]["msg"])
	assert.Equal(t, "error", lines[1]["level"])
}

func TestParseLevel(t *testing.T) {
	assert.Equal(t, LevelDebug, ParseLevel("DEBUG"))
	assert.Equal(t, LevelDebug, ParseLevel(" debug "))
	assert.Equal(t, LevelWarn, ParseLevel("warning"))
	assert.Equal(t, LevelInfo, ParseLevel(""))
	assert.Equal(t, LevelInfo, ParseLevel("bogus"))
	assert.Equal(t, LevelFatal, ParseLevel("fatal"))
}

// 回归：错误值曾走 encoding/json 被序列化成 "{}"，日志里只剩空对象、错误内容全丢。
func TestErrorValueSerializedAsText(t *testing.T) {
	buf := captureDefault(t, Config{Service: "review"})

	ErrorContext(context.Background(), "get account failed",
		"user_id", "u-123", "error", errors.New("connection refused"))
	ErrorContext(context.Background(), "wrapped",
		"error", fmt.Errorf("get account from db: %w", io.EOF))
	ErrorContext(context.Background(), "fatal level",
		"error", fmt.Errorf("chain: %w", errors.New("boom")))

	lines := parseLines(t, buf)
	assert.Equal(t, "connection refused", lines[0]["error"])
	assert.Equal(t, "get account from db: EOF", lines[1]["error"])
	assert.Equal(t, "chain: boom", lines[2]["error"])

	// 显式防线：原始输出里绝不能出现空对象形式的 error。
	assert.NotContains(t, buf.String(), `"error":{}`)
}

// 非 error 的复杂值仍走 JSON 序列化；不可序列化的降级为文本，不丢行。
func TestNonErrorValuesKeepJSONShape(t *testing.T) {
	buf := captureDefault(t, Config{Service: "review", DisableSource: true})

	Info("values",
		"count", 42,
		"ratio", 1.5,
		"ok", true,
		"nested", map[string]int{"a": 1},
		"list", []string{"x", "y"},
		"unsupported", func() {}, // json 无法序列化 → 降级为文本
	)

	fields := parseLines(t, buf)[0]
	assert.Equal(t, float64(42), fields["count"])
	assert.Equal(t, 1.5, fields["ratio"])
	assert.Equal(t, true, fields["ok"])
	assert.Equal(t, map[string]any{"a": float64(1)}, fields["nested"])
	assert.Equal(t, []any{"x", "y"}, fields["list"])
	assert.IsType(t, "", fields["unsupported"])
}

func TestTimeFormatMillisecondUTC(t *testing.T) {
	buf := captureDefault(t, Config{Service: "review"})

	Info("x")

	// 固定 3 位毫秒 + UTC 的 Z 后缀：2026-09-08T07:12:34.567Z
	got := parseLines(t, buf)[0]["time"].(string)
	assert.Regexp(t, `^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$`, got)
}

// logger 字段取自调用位置，且只保留末两级路径（不写构建机绝对路径）。
func TestSourceMapsToLoggerField(t *testing.T) {
	buf := captureDefault(t, Config{Service: "review"})

	Info("caller location")

	fields := parseLines(t, buf)[0]
	src, ok := fields["logger"].(string)
	require.True(t, ok, "默认应输出 logger 字段")
	assert.Contains(t, src, "log/log_test.go:")
}

func TestSourceCanBeDisabled(t *testing.T) {
	buf := captureDefault(t, Config{Service: "review", DisableSource: true})

	Info("no caller location")

	_, has := parseLines(t, buf)[0]["logger"]
	assert.False(t, has)
}

// 契约要求扁平 JSON：WithGroup 不得产生嵌套对象。
func TestGroupAttrsAreFlattened(t *testing.T) {
	l, buf := newLogger(t, Config{Service: "review", DisableSource: true})

	l.WithGroup("http").Info("req", "method", "GET")

	fields := parseLines(t, buf)[0]
	assert.Equal(t, "GET", fields["http.method"])
	_, nested := fields["http"]
	assert.False(t, nested, "不应出现嵌套对象")
}

// 键序 = 契约字段表顺序（便于人眼扫读与按行提取）。
func TestFieldOrderMatchesContract(t *testing.T) {
	buf := captureDefault(t, Config{
		Service: "review", Env: "test", Instance: "pod-1", Community: "openeuler",
	})

	ctx := sdkctx.WithRequestID(context.Background(), "req-1")
	InfoContext(ctx, "ordered", "biz", "z")

	line := strings.TrimSpace(buf.String())
	order := []string{"time", "level", "msg", "service", "env", "instance", "community", "request_id", "logger", "biz"}
	prev := -1
	for _, k := range order {
		idx := strings.Index(line, `"`+k+`":`)
		require.Greaterf(t, idx, prev, "字段 %s 位置不符合契约顺序：%s", k, line)
		prev = idx
	}
}

// 二级 API：slog.Logger 直用路径同样输出契约 JSON。
func TestDirectSlogLogger(t *testing.T) {
	l, buf := newLogger(t, Config{Service: "review", DisableSource: true})

	l.Info("via slog.Logger", "k", "v")

	fields := parseLines(t, buf)[0]
	assert.Equal(t, "review", fields["service"])
	assert.Equal(t, "v", fields["k"])
}

// 并发写出：每行都必须是完整合法 JSON（配合 -race 跑）。
func TestConcurrentWrites(t *testing.T) {
	const goroutines, perGoroutine = 8, 50

	l, buf := newLogger(t, Config{Service: "review", DisableSource: true})

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				l.Info("concurrent", "i", i)
			}
		}()
	}
	wg.Wait()

	lines := parseLines(t, buf)
	assert.Len(t, lines, goroutines*perGoroutine)
}
