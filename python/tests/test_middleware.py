"""middleware 适配器单测：入口 bind community / request_id，请求内日志可见。"""

import io
import json
from typing import Dict, List

import pytest

from obs_sdk import _context, log, middleware as mw


def _fresh_log(buf) -> None:
    log._defaults = None
    log._log_level = 0  # INFO 实际由 init 重设
    import logging
    root = logging.getLogger()
    for h in list(root.handlers):
        if getattr(h, "name", None) == log._SDK_HANDLER_NAME:
            root.removeHandler(h)
    log.init(service="review", community="openeuler", stream=buf)


def _captured(buf: io.StringIO) -> List[Dict]:
    return [json.loads(l) for l in buf.getvalue().strip().splitlines()
            if l.strip()]


# --- FastAPI ---

fastapi = pytest.importorskip("fastapi")


def test_fastapi_middleware_binds_context():
    buf = io.StringIO()
    _fresh_log(buf)

    from fastapi import FastAPI
    from starlette.testclient import TestClient

    app = FastAPI()
    mw.fastapi_wrap(app, resolver=lambda req: "openeuler")

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


# --- Flask ---

flask = pytest.importorskip("flask")


def test_flask_middleware_binds_context():
    buf = io.StringIO()
    _fresh_log(buf)

    app = flask.Flask(__name__)
    mw.flask_middleware(app, resolver=lambda req: "mindspore")

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


# --- Django ---

django = pytest.importorskip("django")


def test_django_middleware_binds_context():
    buf = io.StringIO()
    _fresh_log(buf)

    from django.conf import settings
    if not settings.configured:
        settings.configure(
            DEBUG=True,
            ALLOWED_HOSTS=["testserver"],
            ROOT_URLCONF=__name__,
            MIDDLEWARE=["obs_sdk.middleware.DjangoMiddleware"],
        )
    import django
    django.setup()

    from django.http import HttpResponse

    def ping(request):
        log.get_logger("t").info("inside django view")
        return HttpResponse("ok")

    from django.urls import path, resolve
    from django.test import RequestFactory

    req = RequestFactory().get("/ping", HTTP_X_REQUEST_ID="req-django")
    # 直接经中间件调用视图。
    get_response = lambda r: ping(r)  # noqa: E731
    mw_instance = mw.DjangoMiddleware(get_response)
    resp = mw_instance(req)
    assert resp.status_code == 200

    lines = [l for l in _captured(buf) if l.get("msg") == "inside django view"]
    assert len(lines) == 1
    assert lines[0]["request_id"] == "req-django"
    assert lines[0]["community"] == "openeuler"  # 未覆写 → 部署默认
