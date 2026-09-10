// Package log 提供结构化 JSON 日志（obs-sdk-go 的 log 部分）。
//
// 输出字段遵循 spec/log-format.md 与 spec/common-fields.md：
//   - 单行扁平 JSON（不 pretty）写 stdout，经 log-agent 采集进 LTS；
//   - 常驻字段 service / env / instance / community 在 Init 时注入；
//   - request_id / community 覆盖值 / trace_id / span_id 由 Handler 在
//     Handle(ctx, ...) 时从 ctx 中的 sdkctx 读取，故 InfoContext 等变体自动带上。
//
// 设计约束：
//   - 只提供 kv 形式（msg 常量 + 交替 key/value），**无 printf 变体** —— 与 stdlib
//     log/slog、kratos v3 一致；可查询的数据必须走字段，不能格式化进 msg 文本。
//   - 包级函数形状对齐 kratos v3：Init 之后直接 log.Info(...) / log.InfoContext(ctx, ...)，
//     无需在每个调用点先绑定请求上下文。
//   - error 值统一序列化为 err.Error() 文本（encoding/json 会把多数错误渲染成 "{}"）。
//   - trace_id / span_id 为二期预留注入位，首期恒空、有值才输出，二期接入零返工。
//
// 用法：
//
//	log.Init(log.Config{Service: "robot-universal-review"})
//
//	// 无请求上下文
//	log.Info("server started", "addr", ":8080")
//
//	// 有请求上下文：request_id / community / trace_id 自动附加
//	log.ErrorContext(ctx, "get account failed", "user_id", uid, "error", err)
package log

import (
	"context"
	"io"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/opensourceways/obs-sdk/go/internal/env"
)

// Level 是日志级别，别名到 slog.Level，保证与 slog 生态互通。
type Level = slog.Level

// Leveler / LevelVar 别名，便于调用方无需直接 import log/slog。
type (
	// Leveler 提供日志级别。
	Leveler = slog.Leveler
	// LevelVar 是可运行时变更的级别。
	LevelVar = slog.LevelVar
)

const (
	// LevelDebug 调试级别。
	LevelDebug Level = slog.LevelDebug
	// LevelInfo 信息级别。
	LevelInfo Level = slog.LevelInfo
	// LevelWarn 告警级别。
	LevelWarn Level = slog.LevelWarn
	// LevelError 错误级别。
	LevelError Level = slog.LevelError
	// LevelFatal 致命级别。是否 os.Exit 由调用方决定，日志层只按 "fatal" 输出 level。
	LevelFatal Level = slog.LevelError + 4
)

// ParseLevel 解析级别字符串（大小写不敏感）；不识别的默认 LevelInfo。
func ParseLevel(s string) Level {
	s = strings.ToUpper(strings.TrimSpace(s))
	switch s {
	case "FATAL":
		return LevelFatal
	case "WARNING":
		return LevelWarn
	}
	var level slog.Level
	if err := level.UnmarshalText([]byte(s)); err == nil {
		return level
	}
	return LevelInfo
}

// Config 日志初始化配置。空字段会回退到 OBS_* 环境变量 / 内置默认。
type Config struct {
	// Service 服务名。缺省取 OBS_SERVICE，再否则 "unknown"。
	Service string
	// Env 部署环境。缺省取 OBS_ENV，再否则 "unknown"。
	Env string
	// Instance 实例标识。缺省取 OBS_INSTANCE，再否则 hostname。
	Instance string
	// Community 部署级默认社区。缺省取 OBS_COMMUNITY，再否则 "unknown"。
	Community string
	// Level 最小输出级别（debug/info/warn/error）。空字符串等价 LevelInfo。
	Level string
	// Output 日志输出 Writer。默认 os.Stdout。
	Output io.Writer
	// DisableSource 关闭 logger 字段（调用位置，形如 service/todo.go:51）。
	// 默认输出；高吞吐场景可置 true，省去每次写出前的栈符号化开销。
	DisableSource bool
}

// New 依据配置构建 *slog.Logger（自定义 Handler，输出契约 JSON），不改动全局默认。
func New(cfg Config) *slog.Logger {
	out := cfg.Output
	if out == nil {
		out = os.Stdout
	}
	return slog.New(newHandler(handlerConfig{
		service:       env.Service(cfg.Service),
		env:           env.Env(cfg.Env),
		instance:      env.Instance(cfg.Instance),
		community:     env.Community(cfg.Community),
		level:         ParseLevel(cfg.Level),
		output:        out,
		disableSource: cfg.DisableSource,
	}))
}

// Init 构建 logger 并设为全局默认（包级函数与 slog.Default 此后都走它）。
// 服务启动时调用一次；未调用时包级函数走 slog 的默认（纯文本）Handler。
func Init(cfg Config) {
	SetDefault(New(cfg))
}

// SetDefault 设为全局默认 logger（同步影响 slog.Default）。
func SetDefault(l *slog.Logger) { slog.SetDefault(l) }

// Default 返回当前全局默认 logger。
func Default() *slog.Logger { return slog.Default() }

// With 返回携带附加属性的 logger（镜像 slog.Logger.With，不改原 logger）。
func With(args ...any) *slog.Logger { return slog.With(args...) }

// WithGroup 返回开启分组的 logger（契约要求扁平 JSON，分组以 "name." 前缀平铺）。
func WithGroup(name string) *slog.Logger { return Default().WithGroup(name) }

// Handler 返回默认 logger 的 Handler。
func Handler() slog.Handler { return Default().Handler() }

// Enabled 报告默认 logger 在给定 ctx / level 下是否会输出。
func Enabled(ctx context.Context, level Level) bool { return Default().Enabled(ctx, level) }

// Debug 输出 debug 级日志（无请求上下文）。
func Debug(msg string, args ...any) { log(context.Background(), LevelDebug, msg, args) }

// DebugContext 输出 debug 级日志，并附加 ctx 中的请求级字段。
func DebugContext(ctx context.Context, msg string, args ...any) { log(ctx, LevelDebug, msg, args) }

// Info 输出 info 级日志（无请求上下文）。
func Info(msg string, args ...any) { log(context.Background(), LevelInfo, msg, args) }

// InfoContext 输出 info 级日志，并附加 ctx 中的请求级字段。
func InfoContext(ctx context.Context, msg string, args ...any) { log(ctx, LevelInfo, msg, args) }

// Warn 输出 warn 级日志（无请求上下文）。
func Warn(msg string, args ...any) { log(context.Background(), LevelWarn, msg, args) }

// WarnContext 输出 warn 级日志，并附加 ctx 中的请求级字段。
func WarnContext(ctx context.Context, msg string, args ...any) { log(ctx, LevelWarn, msg, args) }

// Error 输出 error 级日志（无请求上下文）。
func Error(msg string, args ...any) { log(context.Background(), LevelError, msg, args) }

// ErrorContext 输出 error 级日志，并附加 ctx 中的请求级字段。
func ErrorContext(ctx context.Context, msg string, args ...any) { log(ctx, LevelError, msg, args) }

// Log 输出指定级别的日志。
func Log(ctx context.Context, level Level, msg string, args ...any) {
	log(ctx, level, msg, args)
}

// LogAttrs 输出指定级别的日志，属性以已类型化的 slog.Attr 传入。
func LogAttrs(ctx context.Context, level Level, msg string, attrs ...slog.Attr) {
	h := Default().Handler()
	if !h.Enabled(ctx, level) {
		return
	}
	var pcs [1]uintptr
	// 跳过 [runtime.Callers, logAttrs, LogAttrs]，让 logger 字段指向调用方业务代码。
	runtime.Callers(3, pcs[:])
	record := slog.NewRecord(time.Now(), level, msg, pcs[0])
	record.AddAttrs(attrs...)
	_ = h.Handle(ctx, record)
}

// log 是所有包级日志函数的唯一出口。
//
// 这里手工构造 record 而非复用 slog.Logger.Log：后者的 PC 会落在本包包装函数上，
// 导致 logger 字段指错位置。skip=3 跳过 [runtime.Callers, log, 导出函数]，
// 使 PC 指向调用方业务代码 —— 因此所有导出函数必须【直接】调用本函数。
func log(ctx context.Context, level Level, msg string, args []any) {
	h := Default().Handler()
	if !h.Enabled(ctx, level) {
		return
	}
	var pcs [1]uintptr
	runtime.Callers(3, pcs[:])
	record := slog.NewRecord(time.Now(), level, msg, pcs[0])
	record.Add(args...)
	_ = h.Handle(ctx, record)
}
