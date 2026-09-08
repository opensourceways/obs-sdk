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
    c = metrics.counter("meeting_created_total", "created meetings", "kind")
    c.inc(kind="scheduled")                          # community = ctx 覆盖或部署默认
    c.inc(2, kind="cancelled")                       # 业务 label 走关键字/位置均可

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

    def __init__(self, *, service: str, env: str, instance: str,
                 community: str, namespace: str = "") -> None:
        self._common = {"service": service, "env": env, "instance": instance,
                        "community": community}
        self._namespace = namespace
        self._registry = CollectorRegistry(auto_describe=True)

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


# 进程级便捷单例（默认读 OBS_* 环境变量）。
_default: Optional[Metrics] = None


def init(*, service: Optional[str] = None, env: Optional[str] = None,
         instance: Optional[str] = None, community: Optional[str] = None,
         namespace: str = "") -> Metrics:
    """初始化（进程内单例）。空字段读 OBS_* 环境变量。"""
    global _default
    if _default is None:
        _default = Metrics(
            service=_env.service(service),
            env=_env.env(env),
            instance=_env.instance(instance),
            community=_env.community(community),
            namespace=namespace,
        )
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
