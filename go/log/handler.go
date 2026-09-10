package log

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/opensourceways/obs-sdk/go/sdkctx"
)

// 契约字段名（spec/log-format.md 顶层字段表）。集中定义，避免各处拼写漂移。
const (
	keyTime      = "time"
	keyLevel     = "level"
	keyMsg       = "msg"
	keyService   = "service"
	keyEnv       = "env"
	keyInstance  = "instance"
	keyCommunity = "community"
	keyRequestID = "request_id"
	keyTraceID   = "trace_id"
	keySpanID    = "span_id"
	keyLogger    = "logger"
)

// timeFormat 固定 3 位毫秒的 UTC 布局（契约要求 UTC + 固定毫秒精度）。
// 已约定先 .UTC() 再格式化，故 Z07:00 恒输出 "Z"。
const timeFormat = "2006-01-02T15:04:05.000Z07:00"

// handlerConfig 是 Handler 的静态配置，Init 时确定、运行期不变。
type handlerConfig struct {
	service       string
	env           string
	instance      string
	community     string
	level         slog.Level
	output        io.Writer
	disableSource bool
}

// handler 是遵循 spec/log-format.md 的 slog.Handler：把一条记录写成单行扁平 JSON。
//
// 请求级字段（request_id / trace_id / span_id / community 覆盖值）在 Handle 时从
// 传入的 ctx 读取 sdkctx —— 这正是 InfoContext(ctx, ...) 能自动带上请求字段的原因，
// 调用方无需在每个日志点先绑定一个带上下文的 logger。
type handler struct {
	cfg handlerConfig

	// groups 是当前分组前缀（slog WithGroup）；契约要求扁平 JSON，故以 "." 连接后平铺。
	groups []string
	// preAttrs 是 With(...) 预先附加的属性；prefix 记录其写入时的分组前缀。
	preAttrs []preAttr

	// mu 串行化写出，保证并发下整行不被撕裂。所有派生 handler 共享同一把锁。
	mu *sync.Mutex
}

// preAttr 是带分组前缀的预置属性。
type preAttr struct {
	prefix string
	attr   slog.Attr
}

func newHandler(cfg handlerConfig) *handler {
	return &handler{cfg: cfg, mu: &sync.Mutex{}}
}

// Enabled 按初始化级别过滤。
func (h *handler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.cfg.level
}

// WithAttrs 返回携带预置属性的副本（不改原 handler，可并发派生）。
func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return h
	}
	prefix := strings.Join(h.groups, ".")
	nh := h.clone()
	for _, a := range attrs {
		nh.preAttrs = append(nh.preAttrs, preAttr{prefix: prefix, attr: a})
	}
	return nh
}

// WithGroup 返回开启分组的副本。
func (h *handler) WithGroup(name string) slog.Handler {
	if name == "" {
		return h
	}
	nh := h.clone()
	nh.groups = append(nh.groups, name)
	return nh
}

// clone 深拷贝可变切片（共享 mu）。显式拷贝而非共享底层数组，
// 否则并发 WithAttrs/WithGroup 派生会在同一底层数组上竞争写入。
func (h *handler) clone() *handler {
	nh := &handler{cfg: h.cfg, mu: h.mu}
	nh.groups = append(nh.groups, h.groups...)
	nh.preAttrs = append(nh.preAttrs, h.preAttrs...)
	return nh
}

// Handle 按契约字段顺序写出单行 JSON。
func (h *handler) Handle(ctx context.Context, r slog.Record) error {
	var buf bytes.Buffer
	buf.Grow(256)
	o := &jsonObject{buf: &buf}
	buf.WriteByte('{')

	o.str(keyTime, r.Time.UTC().Format(timeFormat))
	o.str(keyLevel, levelName(r.Level))
	o.str(keyMsg, r.Message)
	o.str(keyService, h.cfg.service)
	o.str(keyEnv, h.cfg.env)
	o.str(keyInstance, h.cfg.instance)

	// 请求级字段：community 覆盖值优先于部署级默认；其余为空时省略该键。
	req := sdkctx.From(ctx)
	community := h.cfg.community
	if req.Community != "" {
		community = req.Community
	}
	o.str(keyCommunity, community)
	if req.RequestID != "" {
		o.str(keyRequestID, req.RequestID)
	}
	if req.TraceID != "" {
		o.str(keyTraceID, req.TraceID)
	}
	if req.SpanID != "" {
		o.str(keySpanID, req.SpanID)
	}

	// 调试定位：调用位置来自 record.PC（包级函数已按调用方 PC 构造 record）。
	if !h.cfg.disableSource {
		if src := sourceName(r.PC); src != "" {
			o.str(keyLogger, src)
		}
	}

	// 业务字段：With(...) 预置的在前，记录自带的在后（后者可覆盖前者）。
	for _, pa := range h.preAttrs {
		h.appendAttr(o, pa.prefix, pa.attr)
	}
	prefix := strings.Join(h.groups, ".")
	r.Attrs(func(a slog.Attr) bool {
		h.appendAttr(o, prefix, a)
		return true
	})

	buf.WriteByte('}')
	buf.WriteByte('\n')

	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := h.cfg.output.Write(buf.Bytes())
	return err
}

// appendAttr 把一个属性平铺写入目标对象；分组属性递归展开为 "group.key"。
func (h *handler) appendAttr(o *jsonObject, prefix string, a slog.Attr) {
	a.Value = a.Value.Resolve() // 解开 LogValuer
	if a.Equal(slog.Attr{}) {
		return
	}
	if a.Value.Kind() == slog.KindGroup {
		// 空键分组表示「就地展开」，前缀不变；否则以组名为前缀下钻。
		sub := prefix
		if a.Key != "" {
			sub = joinKey(prefix, a.Key)
		}
		for _, ga := range a.Value.Group() {
			h.appendAttr(o, sub, ga)
		}
		return
	}
	o.value(joinKey(prefix, a.Key), a.Value)
}

func joinKey(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}

// levelName 输出契约要求的小写 level 枚举。
// slog.Level.String() 为大写（INFO），故此处单独映射；自定义级别按就近归类。
func levelName(l slog.Level) string {
	switch {
	case l >= LevelFatal:
		return "fatal"
	case l >= slog.LevelError:
		return "error"
	case l >= slog.LevelWarn:
		return "warn"
	case l >= slog.LevelInfo:
		return "info"
	default:
		return "debug"
	}
}

// sourceName 把 record.PC 解析为调用位置，形如 "service/todo.go:51"。
// 只保留末两级路径，避免把构建机绝对路径写进生产日志。
//
// 这里必须用 runtime.CallersFrames 而非 FuncForPC：record.PC 是 runtime.Callers
// 捕获的返回地址，FuncForPC 会把它归属到【上一层】帧（实测偏移一帧），
// CallersFrames 才按返回地址的正确语义展开（并正确处理内联帧）。
func sourceName(pc uintptr) string {
	if pc == 0 {
		return ""
	}
	var pcs [1]uintptr
	pcs[0] = pc
	frame, _ := runtime.CallersFrames(pcs[:]).Next()
	file, line := frame.File, frame.Line
	if file == "" {
		return ""
	}
	base := filepath.Base(file)
	dir := filepath.Base(filepath.Dir(file))
	if dir == "." || dir == string(filepath.Separator) {
		return base + ":" + strconv.Itoa(line)
	}
	return dir + "/" + base + ":" + strconv.Itoa(line)
}

// jsonObject 按写入顺序拼装 JSON 对象，键序即契约字段表的顺序。
// 不能用 map + json.Marshal：那样键序是按字典序排的，且 error 值会退化成 "{}"。
type jsonObject struct {
	buf   *bytes.Buffer
	count int
}

// key 写入字段名（非首个字段前置逗号）。
func (o *jsonObject) key(k string) {
	if o.count > 0 {
		o.buf.WriteByte(',')
	}
	o.count++
	o.buf.Write(appendJSONString(nil, k))
	o.buf.WriteByte(':')
}

// str 写入字符串字段。
func (o *jsonObject) str(k, v string) {
	o.key(k)
	o.buf.Write(appendJSONString(nil, v))
}

// value 写入 slog.Value 字段。
func (o *jsonObject) value(k string, v slog.Value) {
	o.key(k)
	o.writeValue(v.Any())
}

func (o *jsonObject) writeValue(v any) {
	switch x := v.(type) {
	case nil:
		o.buf.WriteString("null")
	case error:
		// error 一律取原因链文本：stdlib encoding/json 会把多数错误类型渲染成 "{}"，
		// 静默丢掉错误内容。此处是契约里 error 字段的唯一写法。
		o.buf.Write(appendJSONString(nil, x.Error()))
	case time.Time:
		o.buf.Write(appendJSONString(nil, x.UTC().Format(timeFormat)))
	case time.Duration:
		// 输出 "1.5s" 这类可读文本，而非纳秒整数。
		o.buf.Write(appendJSONString(nil, x.String()))
	default:
		b, err := json.Marshal(v)
		if err != nil {
			// 不可序列化的值降级为文本，保证永远不丢日志行。
			o.buf.Write(appendJSONString(nil, fmt.Sprint(v)))
			return
		}
		o.buf.Write(b)
	}
}

// appendJSONString 追加一个 JSON 字符串字面量。
//
// 只转义 JSON 规范要求的字符（`"`、`\`、控制字符），不转义 `<` `>` `&` —— 与
// stdlib log/slog 的 JSONHandler 行为一致，日志可读性更好。非法 UTF-8 交给
// encoding/json 做 U+FFFD 替换，避免产出的行不是合法 JSON 而撑爆 LTS 解析。
func appendJSONString(dst []byte, s string) []byte {
	if !utf8.ValidString(s) {
		b, err := json.Marshal(s)
		if err != nil {
			return append(dst, '"', '"')
		}
		return append(dst, b...)
	}
	const hex = "0123456789abcdef"
	dst = append(dst, '"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '"':
			dst = append(dst, '\\', '"')
		case c == '\\':
			dst = append(dst, '\\', '\\')
		case c == '\n':
			dst = append(dst, '\\', 'n')
		case c == '\r':
			dst = append(dst, '\\', 'r')
		case c == '\t':
			dst = append(dst, '\\', 't')
		case c < 0x20:
			dst = append(dst, '\\', 'u', '0', '0', hex[c>>4], hex[c&0xF])
		default:
			dst = append(dst, c)
		}
	}
	return append(dst, '"')
}
