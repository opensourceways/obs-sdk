"""middleware 适配器单测。

覆盖两件事：
  1. 入口 bind community / request_id，请求内日志可见；
  2. 记 HTTP 服务端指标 obs_http_server_*（spec/metrics-format.md），
     且 path label 取**路由模板**而不是原始 URL —— 原始路径带 ID 会撑爆时序基数。
"""

import io
import json
import logging
from typing import Dict, List

import pytest

from obs_sdk import log, metrics as obs_metrics, middleware as mw


def _fresh_log(buf) -> None:
    log._defaults = None
    log._log_level = 0  # INFO 实际由 init 重设
    root = logging.getLogger()
    for h in list(root.handlers):
        if getattr(h, "name", None) == log._SDK_HANDLER_NAME:
            root.removeHandler(h)
    log.init(service="review", community="openeuler", stream=buf)


def _captured(buf: io.StringIO) -> List[Dict]:
    return [json.loads(l) for l in buf.getvalue().strip().splitlines()
            if l.strip()]


def _new_metrics() -> obs_metrics.Metrics:
    """独立的 Metrics 实例：每个用例互不共享注册表。"""
    return obs_metrics.Metrics(service="review", env="test", instance="pod-1",
                               community="openeuler")


def _rows(m: obs_metrics.Metrics, family: str) -> List[str]:
    return [l for l in m.text().decode().splitlines()
            if l.startswith(family + "{")]


def _reset_singleton() -> None:
    """Django 中间件由框架实例化、拿不到 metrics 参数，只能用进程级单例。"""
    obs_metrics._default = None


# --- FastAPI ---

fastapi = pytest.importorskip("fastapi")


def test_fastapi_middleware_binds_context():
    buf = io.StringIO()
    _fresh_log(buf)
    m = _new_metrics()

    from fastapi import FastAPI
    from starlette.testclient import TestClient

    app = FastAPI()
    mw.fastapi_wrap(app, resolver=lambda req: "openeuler", metrics=m)

    @app.get("/ping")
    def ping():
        log.get_logger("t").info("inside handler")
        return {"ok": True}

    client = TestClient(app)
    resp = client.get("/ping", headers={mw.HEADER_REQUEST_ID: "req-fastapi"})
    assert resp.status_code == 200

    lines = [l for l in _captured(buf) if l.get("msg") == "inside handler"]
    assert len(lines) == 1
    assert lines[0]["community"] == "openeuler"
    assert lines[0]["request_id"] == "req-fastapi"


def test_fastapi_records_server_metrics_with_route_template():
    buf = io.StringIO()
    _fresh_log(buf)
    m = _new_metrics()

    from fastapi import FastAPI
    from starlette.testclient import TestClient

    app = FastAPI()
    mw.fastapi_wrap(app, resolver=lambda req: "mindspore", metrics=m)

    @app.get("/items/{item_id}")
    def item(item_id: str):
        return {"id": item_id}

    client = TestClient(app)
    assert client.get("/items/12345").status_code == 200
    assert client.get("/nope").status_code == 404

    rows = _rows(m, "obs_http_server_requests_total")
    by_path = {r.split('path="')[1].split('"')[0]: r for r in rows}
    # 动态段归一为路由模板；未匹配（404）归到 unmatched，都不进原始路径。
    assert "/items/{item_id}" in by_path, rows
    assert "unmatched" in by_path, rows
    assert not any("12345" in r for r in rows), rows

    matched = by_path["/items/{item_id}"]
    assert 'status_code="200"' in matched
    assert 'method="GET"' in matched
    # community 取请求上下文覆盖值（记在 bind 作用域内）。
    assert 'community="mindspore"' in matched
    assert 'service="review"' in matched

    # 时延直方图同 label 维度，且桶边界与 Go/Node 对齐（钉住 .005 / 10）。
    text = m.text().decode()
    assert "obs_http_server_request_duration_seconds_bucket" in text
    assert 'le="0.005"' in text, text
    assert 'le="10.0"' in text, text
    # prometheus_client 自带默认里多出来的桶不应出现（否则与另两个语言对不上）。
    assert 'le="0.075"' not in text, text


def test_fastapi_records_500_when_view_raises():
    buf = io.StringIO()
    _fresh_log(buf)
    m = _new_metrics()

    from fastapi import FastAPI
    from starlette.testclient import TestClient

    app = FastAPI()
    mw.fastapi_wrap(app, metrics=m)

    @app.get("/boom")
    def boom():
        raise RuntimeError("boom")

    client = TestClient(app, raise_server_exceptions=False)
    assert client.get("/boom").status_code == 500

    rows = _rows(m, "obs_http_server_requests_total")
    assert rows, "视图抛异常也必须记一次，否则漏计"
    assert 'status_code="500"' in rows[0]
    assert 'path="/boom"' in rows[0]


# --- Flask ---

flask = pytest.importorskip("flask")


def test_flask_middleware_binds_context():
    buf = io.StringIO()
    _fresh_log(buf)
    m = _new_metrics()

    app = flask.Flask(__name__)
    mw.flask_middleware(app, resolver=lambda req: "mindspore", metrics=m)

    @app.get("/ping")
    def ping():
        log.get_logger("t").info("inside handler")
        return "ok"

    with app.test_client() as c:
        r = c.get("/ping", headers={mw.HEADER_REQUEST_ID: "req-flask"})
        assert r.status_code == 200

    lines = [l for l in _captured(buf) if l.get("msg") == "inside handler"]
    assert len(lines) == 1
    assert lines[0]["community"] == "mindspore"
    assert lines[0]["request_id"] == "req-flask"


def test_flask_records_server_metrics_with_route_template():
    buf = io.StringIO()
    _fresh_log(buf)
    m = _new_metrics()

    app = flask.Flask(__name__)
    mw.flask_middleware(app, metrics=m)

    @app.get("/items/<int:item_id>")
    def item(item_id):
        return "ok"

    with app.test_client() as c:
        assert c.get("/items/12345").status_code == 200
        assert c.get("/nope").status_code == 404

    rows = _rows(m, "obs_http_server_requests_total")
    by_path = {r.split('path="')[1].split('"')[0]: r for r in rows}
    assert "/items/<int:item_id>" in by_path, rows
    assert "unmatched" in by_path, rows
    assert not any("12345" in r for r in rows), rows


# --- Django ---

django = pytest.importorskip("django")

from django.http import HttpResponse  # noqa: E402  （上方的 importorskip 保证 django 已装）
from django.urls import path  # noqa: E402


def _ping(request, **kwargs):  # 参数化路由会传 pk
    log.get_logger("t").info("inside django view")
    return HttpResponse("ok")


urlpatterns = [path("ping", _ping), path("items/<int:pk>", _ping)]


def _django_setup() -> None:
    from django.conf import settings
    if not settings.configured:
        settings.configure(
            DEBUG=True,
            # 仅测试用；不设的话 Django 渲染调试页时会抛 ImproperlyConfigured，
            # 把真正的失败原因盖掉。
            SECRET_KEY="test-only-not-a-secret",
            ALLOWED_HOSTS=["testserver"],
            ROOT_URLCONF=__name__,
            MIDDLEWARE=["obs_sdk.middleware.DjangoMiddleware"],
        )
    import django as _django
    _django.setup()


def test_django_middleware_binds_context():
    buf = io.StringIO()
    _fresh_log(buf)
    _reset_singleton()
    _django_setup()

    from django.test import Client

    client = Client()
    resp = client.get("/ping", HTTP_X_REQUEST_ID="req-django")
    assert resp.status_code == 200

    lines = [l for l in _captured(buf) if l.get("msg") == "inside django view"]
    assert len(lines) == 1
    assert lines[0]["request_id"] == "req-django"
    assert lines[0]["community"] == "openeuler"  # 未覆写 → 部署默认


def test_django_records_server_metrics_with_route_template():
    buf = io.StringIO()
    _fresh_log(buf)
    _reset_singleton()
    _django_setup()

    from django.test import Client

    client = Client()
    assert client.get("/items/12345").status_code == 200
    assert client.get("/nope").status_code == 404

    m = obs_metrics.default()  # DjangoMiddleware 由框架实例化，用的是单例
    rows = _rows(m, "obs_http_server_requests_total")
    by_path = {r.split('path="')[1].split('"')[0]: r for r in rows}
    assert "items/<int:pk>" in by_path, rows
    assert "unmatched" in by_path, rows
    assert not any("12345" in r for r in rows), rows
