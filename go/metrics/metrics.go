// Package metrics 提供 Prometheus 指标装配（obs-sdk-go 的 metrics 部分）。
//
// 底层使用官方库 github.com/prometheus/client_golang，SDK 不自研
// instrumentation，只做装配与命名/label 对齐（见 spec/metrics-format.md）：
//   - 所有指标自动附加 const label：service / env / instance / community(默认值)；
//   - community 是唯一可动态的公共 label：中心化多社区服务注册
//     Community 动态 Vec，打点时可取请求覆盖值，未覆盖回退部署级默认；
//   - 统一暴露 /metrics Handler（Prometheus text exposition）。
//
// 两类形态（见 spec：同一套字段、两种取值来源）：
//  1. 静态形态（单社区独立部署，community 作 const label）：
//
//		m := metrics.New(metrics.Config{Service: "review"})
//		http.Handle("/metrics", m.Handler())
//		c := m.CounterVec("http_requests_total", "http requests", "method")
//		c.WithLabelValues("GET").Add(1)
//
//  2. 动态形态（中心化多社区，community 作可变 label，双层注入）：
//
//		dyn := m.CounterVecCommunity("http_requests_total", "http requests", "method")
//		dyn.WithDefault("GET").Add(1)                 // 部署级默认 community
//		dyn.WithContext(ctx, "GET").Add(1)            // ctx 覆盖 community
package metrics

import (
	"context"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/opensourceways/obs-sdk/go/internal/env"
	"github.com/opensourceways/obs-sdk/go/sdkctx"
)

const (
	// CommunityLabel 是 community 公共 label 的键名（唯一可动态项）。
	CommunityLabel = "community"

	labelService  = "service"
	labelEnv      = "env"
	labelInstance = "instance"
)

// Config 指标初始化配置。空字段回退到 OBS_* 环境变量 / 内置默认。
type Config struct {
	// Service 服务名。
	Service string
	// Env 部署环境。
	Env string
	// Instance 实例标识。
	Instance string
	// Community 部署级默认社区（const label / 动态覆盖回退值）。
	Community string
	// Namespace 可选指标命名空间，参与 FQName 拼接。
	Namespace string
	// IncludeProcess 是否注册 go/process 运行时采集器（默认 true）。
	IncludeProcess *bool
}

// Metrics 是注册中心与装配入口。持有一个独立 prometheus.Registry，避免与
// 全局 DefaultRegisterer 冲突。
type Metrics struct {
	reg *prometheus.Registry

	service          string
	envName          string
	instance         string
	defaultCommunity string
	namespace        string
}

// New 创建装配实例。
func New(cfg Config) *Metrics {
	reg := prometheus.NewRegistry()

	inc := cfg.IncludeProcess == nil || *cfg.IncludeProcess
	if inc {
		reg.MustRegister(collectors.NewGoCollector())
		reg.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	}

	return &Metrics{
		reg:              reg,
		service:          env.Service(cfg.Service),
		envName:          env.Env(cfg.Env),
		instance:         env.Instance(cfg.Instance),
		defaultCommunity: env.Community(cfg.Community),
		namespace:        cfg.Namespace,
	}
}

// Registry 返回底层 prometheus.Registry（高级用法 / 测试）。
func (m *Metrics) Registry() *prometheus.Registry { return m.reg }

// Handler 返回 /metrics HTTP handler（Prometheus text exposition）。
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
}

// Service 返回注入的服务名。
func (m *Metrics) Service() string { return m.service }

// DefaultCommunity 返回部署级默认社区。
func (m *Metrics) DefaultCommunity() string { return m.defaultCommunity }

// constLabels 返回自动附加的 const label：service/env/instance + community 默认值。
func (m *Metrics) constLabels() prometheus.Labels {
	return prometheus.Labels{
		labelService:   m.service,
		labelEnv:       m.envName,
		labelInstance:  m.instance,
		CommunityLabel: m.defaultCommunity,
	}
}

// communityConstOnly 返回仅含 service/env/instance 的 const label
// （community 将由动态 Vec 作为可变 label 自行声明）。
func (m *Metrics) constLabelsNoCommunity() prometheus.Labels {
	return prometheus.Labels{
		labelService:  m.service,
		labelEnv:      m.envName,
		labelInstance: m.instance,
	}
}

// fqName 拼 FQName：namespace[.subsystem.]name，去空段。
func (m *Metrics) fqName(name string) string {
	return prometheus.BuildFQName(m.namespace, "", name)
}

// communityFromContext 解析 community：ctx 覆盖优先，否则部署默认。
// ctx 为 nil 视为无覆盖。
func (m *Metrics) communityFromContext(ctx context.Context) string {
	if ctx == nil {
		return m.defaultCommunity
	}
	if c := sdkctx.Community(ctx); c != "" {
		return c
	}
	return m.defaultCommunity
}
