package metrics

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
)

// 设计说明（对应 spec/metrics-format.md「community 双层注入在指标上的实现」）：
// community 统一声明为可变 label（非 const），这样单社区服务注册后每次打点
// 填部署默认值、中心化多社区服务可填请求覆盖值 —— 「注册一次，两用」。
// service / env / instance 恒为 const label，随 Vec 自动附加。

// community + business 全量 label 名。
func (m *Metrics) allLabelNames(business []string) []string {
	out := make([]string, 0, len(business)+1)
	out = append(out, CommunityLabel)
	out = append(out, business...)
	return out
}

// resolve 构造一次性 label 值：community 放首位，后接业务 label 值。
func (m *Metrics) resolve(ctx context.Context, businessVals []string) []string {
	out := make([]string, 0, len(businessVals)+1)
	out = append(out, m.communityFromContext(ctx))
	out = append(out, businessVals...)
	return out
}

// --- Counter ---

// CounterVec 是业务计数器。注册时自动附加 service/env/instance const label 与
// community 可变 label。同名重复注册会 panic（与 client_golang 语义一致）。
type CounterVec struct {
	m   *Metrics
	vec *prometheus.CounterVec
}

// NewCounterVec 注册一个计数器。businessLabels 为业务维度（不含 community）。
func (m *Metrics) NewCounterVec(name, help string, businessLabels ...string) *CounterVec {
	vec := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name:        m.fqName(name),
			Help:        help,
			ConstLabels: m.constLabelsNoCommunity(),
		},
		m.allLabelNames(businessLabels),
	)
	m.reg.MustRegister(vec)
	return &CounterVec{m: m, vec: vec}
}

// Inc 以部署级默认 community 自增。businessVals 顺序须与注册时 businessLabels 一致。
func (v *CounterVec) Inc(businessVals ...string) { v.Add(1, businessVals...) }

// Add 以部署级默认 community 增加 v。businessVals 同上。
func (v *CounterVec) Add(delta float64, businessVals ...string) {
	v.vec.WithLabelValues(v.m.resolve(nil, businessVals)...).Add(delta)
}

// IncWithContext 以 ctx 覆盖 community 自增（未覆盖回退默认）。
func (v *CounterVec) IncWithContext(ctx context.Context, businessVals ...string) {
	v.AddWithContext(ctx, 1, businessVals...)
}

// AddWithContext 以 ctx 覆盖 community 增加 v。
func (v *CounterVec) AddWithContext(ctx context.Context, delta float64, businessVals ...string) {
	v.vec.WithLabelValues(v.m.resolve(ctx, businessVals)...).Add(delta)
}

// Vec 返回底层 CounterVec（高级用法；注意 community 为首个 label）。
func (v *CounterVec) Vec() *prometheus.CounterVec { return v.vec }

// --- Gauge ---

// GaugeVec 与 CounterVec 同构，取 gauge 语义。
type GaugeVec struct {
	m   *Metrics
	vec *prometheus.GaugeVec
}

// NewGaugeVec 注册一个 gauge。businessLabels 为业务维度（不含 community）。
func (m *Metrics) NewGaugeVec(name, help string, businessLabels ...string) *GaugeVec {
	vec := prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name:        m.fqName(name),
			Help:        help,
			ConstLabels: m.constLabelsNoCommunity(),
		},
		m.allLabelNames(businessLabels),
	)
	m.reg.MustRegister(vec)
	return &GaugeVec{m: m, vec: vec}
}

// Set 以部署级默认 community 设值。
func (v *GaugeVec) Set(val float64, businessVals ...string) {
	v.vec.WithLabelValues(v.m.resolve(nil, businessVals)...).Set(val)
}

// SetWithContext 以 ctx 覆盖 community 设值。
func (v *GaugeVec) SetWithContext(ctx context.Context, val float64, businessVals ...string) {
	v.vec.WithLabelValues(v.m.resolve(ctx, businessVals)...).Set(val)
}

// Inc 以部署级默认 community 自增 1（gauge 语义）。
func (v *GaugeVec) Inc(businessVals ...string) { v.Add(1, businessVals...) }

// Add 以部署级默认 community 增减 v。
func (v *GaugeVec) Add(delta float64, businessVals ...string) {
	v.vec.WithLabelValues(v.m.resolve(nil, businessVals)...).Add(delta)
}

// Vec 返回底层 GaugeVec。
func (v *GaugeVec) Vec() *prometheus.GaugeVec { return v.vec }

// --- Histogram ---

// HistogramVec 与 CounterVec 同构，取 histogram 语义（默认桶见 NewHistogramVec 注释）。
type HistogramVec struct {
	m   *Metrics
	vec *prometheus.HistogramVec
}

// NewHistogramVec 注册一个 histogram（默认 Prometheus 桶）。
func (m *Metrics) NewHistogramVec(name, help string, businessLabels ...string) *HistogramVec {
	return m.NewHistogramVecWithBuckets(name, help, nil, businessLabels...)
}

// NewHistogramVecWithBuckets 注册带自定义桶的 histogram。
func (m *Metrics) NewHistogramVecWithBuckets(name, help string, buckets []float64, businessLabels ...string) *HistogramVec {
	vec := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:        m.fqName(name),
			Help:        help,
			Buckets:     buckets,
			ConstLabels: m.constLabelsNoCommunity(),
		},
		m.allLabelNames(businessLabels),
	)
	m.reg.MustRegister(vec)
	return &HistogramVec{m: m, vec: vec}
}

// Observe 以部署级默认 community 记录一个观测值。
func (v *HistogramVec) Observe(val float64, businessVals ...string) {
	v.vec.WithLabelValues(v.m.resolve(nil, businessVals)...).Observe(val)
}

// ObserveWithContext 以 ctx 覆盖 community 记录观测值。
func (v *HistogramVec) ObserveWithContext(ctx context.Context, val float64, businessVals ...string) {
	v.vec.WithLabelValues(v.m.resolve(ctx, businessVals)...).Observe(val)
}

// Vec 返回底层 HistogramVec。
func (v *HistogramVec) Vec() *prometheus.HistogramVec { return v.vec }
