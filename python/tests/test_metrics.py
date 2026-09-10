"""metrics 模块单测：公共 label 注入、community 双层注入、text 输出。"""

import re

import pytest

from obs_sdk import _context, metrics


@pytest.fixture(autouse=True)
def _clean_default():
    # 每个用例重置全局单例，避免注册表重复名称冲突。
    metrics._default = None
    yield
    metrics._default = None


def _new(**cfg):
    defaults = dict(service="srv", env="test", instance="pod-1",
                    community="openeuler", namespace="")
    defaults.update(cfg)
    return metrics.Metrics(**defaults)


def test_counter_common_labels():
    m = _new()
    c = m.counter("events_total", "events", ["kind"])
    c.inc(kind="pr")

    text = m.text().decode()
    assert "# HELP events_total events" in text
    rows = [l for l in text.splitlines()
            if re.match(r"^events_total\{", l)]
    assert len(rows) == 1
    assert 'service="srv"' in rows[0]
    assert 'env="test"' in rows[0]
    assert 'instance="pod-1"' in rows[0]
    assert 'community="openeuler"' in rows[0]
    assert 'kind="pr"' in rows[0]
    assert rows[0].endswith(" 1.0")


def test_community_double_layer():
    m = _new(community="openeuler")
    c = m.counter("events_total", "events", ["kind"])

    c.inc(kind="pr")                      # 默认 openeuler
    with _context.bind(community="mindspore"):
        c.inc(kind="pr")                  # ctx 覆盖 → mindspore

    rows = [l for l in m.text().decode().splitlines()
            if re.match(r"^events_total\{", l)]
    assert len(rows) == 2
    by_comm = {r.split('community="')[1].split('"')[0]: r for r in rows}
    assert by_comm["openeuler"].endswith(" 1.0")
    assert by_comm["mindspore"].endswith(" 1.0")


def test_gauge_and_histogram():
    m = _new()
    g = m.gauge("in_flight", "in flight")
    g.set(3)
    g.inc(2)
    rows = [l for l in m.text().decode().splitlines()
            if re.match(r"^in_flight\{", l)]
    assert rows[0].endswith(" 5.0")

    h = m.histogram("latency_seconds", "latency")
    h.observe(0.1)
    h.observe(0.2)
    text = m.text().decode()
    assert "latency_seconds_count" in text
    count = [l for l in text.splitlines() if re.match(r"^latency_seconds_count\{", l)]
    assert count[0].endswith(" 2.0")


def test_metrics_singleton_env():
    # 便捷单例 init 幂等。
    metrics.init(service="srv", community="ascend")
    a = metrics.default()
    metrics.init(service="ignored")  # 已建不重建
    assert metrics.default() is a
    assert metrics.default().default_community == "ascend"
