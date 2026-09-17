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


def test_metrics_init_always_rebuilds(monkeypatch):
    # 与 log.init 统一：可重复调用，最后一次生效（重建实例与注册表）。
    for key in ("OBS_SERVICE", "OBS_ENV", "OBS_INSTANCE", "OBS_COMMUNITY"):
        monkeypatch.delenv(key, raising=False)
    first = metrics.init(service="srv", community="ascend")
    assert metrics.default() is first
    second = metrics.init(service="srv", community="mindspore")
    assert second is not first
    assert metrics.default() is second
    assert metrics.default().default_community == "mindspore"


def test_fields_fallback_when_not_configured(monkeypatch):
    # 同 Java：构造时即完成三级解析，空 label 不可接受 —— 空值会被注册表跳过，
    # 该 series 与其它语言对不上，按 label 过滤时静默漏数。
    for key in ("OBS_SERVICE", "OBS_ENV", "OBS_INSTANCE", "OBS_COMMUNITY"):
        monkeypatch.delenv(key, raising=False)
    m = metrics.Metrics()  # 一个参数都不给
    m.counter("events_total", "events", ["kind"]).inc(kind="pr")

    rows = [l for l in m.text().decode().splitlines()
            if re.match(r"^events_total\{", l)]
    assert len(rows) == 1
    for label in ("service", "env", "instance", "community"):
        assert f'{label}=""' not in rows[0], rows[0]
        assert f'{label}="' in rows[0], rows[0]


def test_http_server_handle_registered_once():
    # http_server() 幂等：prometheus_client 同名重复注册会抛 ValueError，
    # 缓存句柄是中间件能在多请求下存活的唯一保障。
    m = _new()
    server = m.http_server()
    assert m.http_server() is server

    server.observe_request(method="GET", path="/items/{item_id}",
                           status_code=200, seconds=0.01)
    text = m.text().decode()
    assert "obs_http_server_requests_total" in text
    assert "obs_http_server_request_duration_seconds_bucket" in text
    # 路由模板原样落成 label —— 中间件传什么就是什么。
    assert 'path="/items/{item_id}"' in text
    # 桶边界显式固定，含 prometheus_client 默认没有的 .005 / 10，且不含其默认多出的 .075。
    assert 'le="0.005"' in text, text
    assert 'le="10.0"' in text, text
    assert 'le="0.075"' not in text, text
