"""Prometheus 指标装配（obs-sdk-python 的 metrics 部分）。

底层用官方库 prometheus-client，SDK 不自研 instrumentation，只做装配与
命名/label 对齐（见 spec/metrics-format.md）：
  - 所有指标自动带固定 label：service / env / instance / community；
  - community 是唯一可动态的公共 label：ctx 覆盖优先，否则部署默认
    （双层注入，"注册一次，两用"）；
  - 统一暴露文本输出（metrics.text() / 便捷 generate_text()）。

用法：

    from obs_sdk import metrics
    metrics.init(service="meeting-center")          # 或读 OBS_* 环境变量
    c = metrics.counter("meeting_created_total", "created meetings", ["kind"])
    c.inc(kind="scheduled")                          # community = ctx 覆盖或部署默认
    c.inc(2, kind="cancelled")                       # 业务 label 按关键字传

    # /metrics 文本：
    from obs_sdk import metrics
    text = metrics.generate_text()
"""

from __future__ import annotations

from typing import Dict, List, Optional

from prometheus_client import (CONTENT_TYPE_LATEST, CollectorRegistry,
                               Counter, Gauge, Histogram, generate_latest)

from . import _context, _env

# 常驻公共 label 键。
_COMMON = ["service", "env", "instance", "community"]


class _Vec:
    """Vec 薄封装：注册时声明业务 label，打点时自动填公共维度。"""

    def __init__(self, metric, common_values: Dict[str, str]) -> None:
        self._metric = metric
        self._common = common_values  # service/env/instance 值（community 动态取）

    def _labels(self, labelvalues: Dict[str, str]) -> object:
        req = _context.get()
        community = req.community or self._common["community"]
        vals = dict(self._common)
        vals["community"] = community
        vals.update(labelvalues)
        return self._metric.labels(**vals)

    # --- counter ---

    def inc(self, amount: float = 1.0, **labelvalues: str) -> None:
        self._labels(labelvalues).inc(amount)

    # --- gauge ---

    def set(self, value: float, **labelvalues: str) -> None:
        self._labels(labelvalues).set(value)

    # --- histogram ---

    def observe(self, value: float, **labelvalues: str) -> None:
        self._labels(labelvalues).observe(value)


class Metrics:
    """装配入口：持有独立 CollectorRegistry，避免全局注册表相互污染。"""

    def __init__(self, *, service: Optional[str] = None, env: Optional[str] = None,
                 instance: Optional[str] = None, community: Optional[str] = None,
                 namespace: str = "") -> None:
        # 构造时即完成三级解析（显式参数 > OBS_* > 内置默认），与 Go/Java 一致：
        # 空 label 不可接受 —— 不设兜底就会出现 service=""，同一条 series 与其它语言
        # 对不上，按 label 过滤时静默漏数。见 spec/common-fields.md。
        self._common = {"service": _env.service(service), "env": _env.env(env),
                        "instance": _env.instance(instance),
                        "community": _env.community(community)}
        self._namespace = namespace
        self._registry = CollectorRegistry(auto_describe=True)
        self._http_server: Optional["_HttpServer"] = None

    @property
    def registry(self) -> CollectorRegistry:
        return self._registry

    @property
    def default_community(self) -> str:
        return self._common["community"]

    def _fq(self, name: str) -> str:
        return f"{self._namespace}_{name}" if self._namespace else name

    def _collector(self, cls, name: str, documentation: str,
                   labelnames: List[str], **kw) -> _Vec:
        metric = cls(self._fq(name), documentation,
                     labelnames=[*_COMMON, *labelnames],
                     registry=self._registry, **kw)
        return _Vec(metric, self._common)

    def counter(self, name: str, documentation: str,
                labelnames: Optional[List[str]] = None) -> _Vec:
        return self._collector(Counter, name, documentation, list(labelnames or []))

    def gauge(self, name: str, documentation: str,
              labelnames: Optional[List[str]] = None) -> _Vec:
        return self._collector(Gauge, name, documentation, list(labelnames or []))

    def histogram(self, name: str, documentation: str,
                  labelnames: Optional[List[str]] = None,
                  buckets: Optional[List[float]] = None) -> _Vec:
        kw: Dict = {}
        if buckets is not None:
            kw["buckets"] = buckets
        return self._collector(Histogram, name, documentation,
                               list(labelnames or []), **kw)

    def text(self) -> bytes:
        """输出该注册表 Prometheus text 格式。"""
        return generate_latest(self._registry)

    def http_server(self) -> "_HttpServer":
        """中间件 HTTP 服务端公共指标句柄（懒注册，同一实例只注册一次）。

        注册必须只发生一次：prometheus_client 对同名指标的重复注册会直接抛
        ValueError，所以缓存放在这里，三个框架适配器共用。
        """
        if self._http_server is None:
            self._http_server = _HttpServer(self)
        return self._http_server


# 中间件公共指标名（spec/metrics-format.md「默认暴露的中间件指标」）：
# 共享 SDK 中间件统一用 SDK 保留前缀 obs_，不以 service 名开头
# —— 同一条 series 已带 service label，查询按 label 过滤。
SERVER_REQUESTS_TOTAL = "obs_http_server_requests_total"
SERVER_REQUEST_DURATION_SECONDS = "obs_http_server_request_duration_seconds"

# 显式钉住桶边界：与 Go（client_golang 默认）/ Node（DEFAULT_METRIC_BUCKETS）对齐。
# prometheus_client 自带默认多了 .075/.75/7.5 三个点，不钉就会与另两个语言不一致。
SERVER_DURATION_BUCKETS = [0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10]


class _HttpServer:
    """HTTP 服务端公共指标：请求计数 + 时延（秒）。

    label 除四个公共维度外为 method / path / status_code。**path 必须传路由模板
    （如 `/items/{item_id}`）而不是原始 URL** —— 原始路径带 ID 会撑爆时序基数
    （spec/metrics-format.md 明示）。拿不到模板时由调用方传 "unmatched"。
    """

    def __init__(self, m: "Metrics") -> None:
        labels = ["method", "path", "status_code"]
        self._total = m.counter(SERVER_REQUESTS_TOTAL, "HTTP requests handled", labels)
        self._duration = m.histogram(SERVER_REQUEST_DURATION_SECONDS,
                                     "HTTP request latency", labels,
                                     buckets=SERVER_DURATION_BUCKETS)

    def observe_request(self, *, method: str, path: str,
                        status_code: int, seconds: float) -> None:
        """记一次请求。必须在请求上下文仍绑定时调用 —— community label 取自上下文。"""
        labels = {"method": method, "path": path, "status_code": str(status_code)}
        self._total.inc(1, **labels)
        self._duration.observe(seconds, **labels)


# 进程级便捷单例（默认读 OBS_* 环境变量）。
_default: Optional[Metrics] = None


def init(*, service: Optional[str] = None, env: Optional[str] = None,
         instance: Optional[str] = None, community: Optional[str] = None,
         namespace: str = "") -> Metrics:
    """初始化进程级单例。空字段读 OBS_* 环境变量，未设置回退内置默认。

    与 `log.init` 语义一致：**可重复调用，最后一次生效**（重建实例与注册表）。
    重建会换掉底层 CollectorRegistry —— 重建前注册的指标随之作废，且先前取到的
    _Vec 句柄仍指向旧注册表。所以请在进程启动时调用一次；测试里可用它重置状态。
    """
    global _default
    _default = Metrics(service=service, env=env, instance=instance,
                       community=community, namespace=namespace)
    return _default


def default() -> Metrics:
    if _default is None:
        return init()
    return _default


def counter(name: str, documentation: str,
            labelnames: Optional[List[str]] = None) -> _Vec:
    return default().counter(name, documentation, labelnames)


def gauge(name: str, documentation: str,
          labelnames: Optional[List[str]] = None) -> _Vec:
    return default().gauge(name, documentation, labelnames)


def histogram(name: str, documentation: str,
              labelnames: Optional[List[str]] = None) -> _Vec:
    return default().histogram(name, documentation, labelnames)


def generate_text() -> bytes:
    """输出默认注册表 Prometheus text（供 /metrics 路由）。"""
    return default().text()


def content_type() -> str:
    return CONTENT_TYPE_LATEST
