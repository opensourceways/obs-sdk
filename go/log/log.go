// Package log 提供结构化 JSON 日志（obs-sdk-go 的 log 部分）。
//
// 输出格式与字段遵循 spec/log-format.md 与 spec/common-fields.md：
//   - 单行 JSON（不 pretty），经 stdout 采集进 LTS；
//   - 常驻字段 service / env / instance / community 在进程启动时注入；
//   - request_id / community 覆盖值 / trace_id（预留）在请求上下文存在时附加；
//   - trace_id 预留注入位：二期 trace 经 sdkctx.WithTraceID 注入即可见，零返工。
//
// 用法：
//
//	l := log.New(log.Config{Service: "robot-universal-review"})
//	l.Info("handle webhook", "event", "pull_request", "action", "opened")
//
// 请求级覆盖：
//
//	l := log.New(log.Config{Service: "srv"})
//	ctx := sdkctx.WithCommunity(r.Context(), "openeuler")
//	l.WithRequest(ctx).Info("by community", "path", r.URL.Path)
package log

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/opensourceways/obs-sdk/go/internal/env"
	"github.com/opensourceways/obs-sdk/go/sdkctx"
)

// Level 日志级别。
type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

// ParseLevel 解析级别字符串；不识别的默认 LevelInfo。
func ParseLevel(s string) Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return LevelDebug
	case "warn", "warning":
		return LevelWarn
	case "error":
		return LevelError
	default:
		return LevelInfo
	}
}

func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "debug"
	case LevelWarn:
		return "warn"
	case LevelError:
		return "error"
	default:
		return "info"
	}
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
	// Level 最小输出级别。空字符串等价 LevelInfo。
	Level string
	// Output 日志输出 Writer。默认 os.Stdout。
	Output io.Writer
}

// Logger 结构化 JSON 日志器。方法并发安全。
type Logger struct {
	mu sync.Mutex

	service  string
	envName  string
	instance string
	// community 为部署级默认值；请求覆盖值在处理时优先。
	community string
	level     Level
	out       io.Writer

	// req 保存 WithRequest 绑定的请求级字段；nil 表示未绑定。
	req *sdkctx.Request

	// baseFields 是 With(kv...) 附加的字段，构建器模式叠加。
	baseFields map[string]any
}

// New 创建日志器。缺省配置取环境变量回退。
func New(cfg Config) *Logger {
	out := cfg.Output
	if out == nil {
		out = os.Stdout
	}
	return &Logger{
		service:    env.Service(cfg.Service),
		envName:    env.Env(cfg.Env),
		instance:   env.Instance(cfg.Instance),
		community:  env.Community(cfg.Community),
		level:      ParseLevel(cfg.Level),
		out:        out,
		baseFields: map[string]any{},
	}
}

// Service 返回注入的服务名。
func (l *Logger) Service() string { return l.service }

// Community 返回部署级默认社区。
func (l *Logger) Community() string { return l.community }

// With 返回携带附加字段的子日志器（叠加不改原日志器）。
func (l *Logger) With(kv ...any) *Logger {
	cp := l.clone()
	for k, v := range toMap(kv) {
		cp.baseFields[k] = v
	}
	return cp
}

// WithRequest 返回绑定请求上下文的子日志器：该请求日志自动携带
// sdkctx 中 request_id / community（覆盖）/ trace_id（预留）。
func (l *Logger) WithRequest(ctx context.Context) *Logger {
	cp := l.clone()
	if ctx != nil {
		if v := sdkctx.From(ctx); v != (sdkctx.Request{}) {
			cp.req = &v
		}
	}
	return cp
}

func (l *Logger) clone() *Logger {
	cp := &Logger{
		service:    l.service,
		envName:    l.envName,
		instance:   l.instance,
		community:  l.community,
		level:      l.level,
		out:        l.out,
		req:        l.req,
		baseFields: make(map[string]any, len(l.baseFields)+4),
	}
	for k, v := range l.baseFields {
		cp.baseFields[k] = v
	}
	return cp
}

// --- 级别方法 ---

func (l *Logger) Debug(msg string, kv ...any) { l.log(LevelDebug, msg, kv) }
func (l *Logger) Info(msg string, kv ...any)  { l.log(LevelInfo, msg, kv) }
func (l *Logger) Warn(msg string, kv ...any)  { l.log(LevelWarn, msg, kv) }
func (l *Logger) Error(msg string, kv ...any) { l.log(LevelError, msg, kv) }

func (l *Logger) log(level Level, msg string, kv []any) {
	if level < l.level {
		return
	}

	fields := make(map[string]any, len(l.baseFields)+len(kv)/2+8)
	for k, v := range l.baseFields {
		fields[k] = v
	}
	for k, v := range toMap(kv) {
		fields[k] = v
	}

	// 请求级字段注入（覆盖静态默认）。
	if l.req != nil {
		if l.req.Community != "" {
			fields["community"] = l.req.Community
		}
		if l.req.RequestID != "" {
			fields["request_id"] = l.req.RequestID
		}
		if l.req.TraceID != "" {
			fields["trace_id"] = l.req.TraceID
		}
	}

	// 常驻字段。community 未被请求覆盖时用部署级默认。
	if _, ok := fields["community"]; !ok {
		fields["community"] = l.community
	}
	fields["service"] = l.service
	fields["env"] = l.envName
	fields["instance"] = l.instance
	fields["level"] = level.String()
	fields["msg"] = msg
	fields["time"] = time.Now().UTC().Format(time.RFC3339Nano)

	line, err := json.Marshal(fields)
	if err != nil {
		line, _ = json.Marshal(map[string]any{
			"level": level.String(), "msg": msg, "service": l.service,
			"error": fmt.Sprintf("marshal log fields: %v", err),
		})
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = l.out.Write(append(line, '\n'))
}

// toMap 把 kv... 对拍平为 map；奇数长度忽略末尾无键值。
func toMap(kv []any) map[string]any {
	m := map[string]any{}
	for i := 0; i+1 < len(kv); i += 2 {
		key, ok := kv[i].(string)
		if !ok {
			continue
		}
		m[key] = kv[i+1]
	}
	return m
}
