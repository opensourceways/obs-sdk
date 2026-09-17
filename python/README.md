# obs-sdk-python

opensourceways 微服务可观测薄封装 SDK 的 Python 实现，契约见 [spec/](../spec/README.md)。

- **日志**：`obs_sdk.log` —— 结构化 JSON（root logger 挂唯一 JsonHandler，字段规范见 spec/log-format.md）
- **指标**：`obs_sdk.metrics` —— prometheus-client 薄封装，自带独立 CollectorRegistry
- **请求上下文**：`obs_sdk._context` —— `contextvars` 承载 `community/request_id/trace_id/span_id`（后两者为二期 trace 预留位）
- **框架适配**：`obs_sdk.middleware` —— FastAPI / Flask / Django 中间件（注入 request_id + 可信判定点解析 community + 记 HTTP 服务端指标）
- **community 双层注入**：`service/env/instance` 常驻 const；`community` 可变 —— 请求上下文覆盖（`_context.bind`），未覆盖回退部署默认（`OBS_*` 环境变量）

## 日志用法

```python
import logging
from obs_sdk import log

# 三级解析：显式参数 > OBS_SERVICE / OBS_ENV / OBS_INSTANCE / OBS_COMMUNITY > 内置默认
# 可重复调用，最后一次生效（重建 handler）；请在进程启动时调用一次
log.init(service="review", env="test", instance="pod-1", community="openeuler")

logger = log.get_logger(__name__)          # 命名 logger，propagate 到 root 的 JSON handler
logger.info("job done", extra={"event": "release", "issue": "2061"})
# 或便捷函数：log.info("job done", extra=...)
```

单条 JSON 行固定键 `service/env/instance/community/level/time/msg` + extra；请求级字段经上下文覆盖。

## 指标用法

```python
from obs_sdk import metrics

metrics.init(service="review", env="test", instance="pod-1", community="openeuler")

built = metrics.counter("built_releases", "发布的构建数", ["kind"])   # 业务 label；community 自动补
built.inc(1, kind="tag")
metrics.gauge("in_flight", "在飞请求数").set(3)
metrics.histogram("review_duration", "评审耗时", ["api"]).observe(0.2)

# FastAPI 暴露 /metrics：
# from obs_sdk import metrics
# return Response(content=metrics.generate_text(), media_type=metrics.content_type())
```

`metrics.init()` 与 `log.init()` 语义一致：可重复调用、最后一次生效（重建实例与注册表）；
字段同样走三级解析，`Metrics(...)` 不传参数也不会留下空 label。多注册表场景直接 `Metrics(...)`。

> 重建会换掉底层 `CollectorRegistry`：重建前注册的指标随之作废，且先前取到的 `_Vec` 句柄仍指向旧注册表。
> 所以请在进程启动时调用一次；测试里可用它重置状态。

## 请求上下文 / 中间件（community 双层注入）

请求级 community 只在**服务可信判定点**（路由前缀 / 认证主体 / 白名单）解析后显式 bind，
不从不加鉴别的 URL / Header 盲取（见 spec/community-values.md）。

```python
from obs_sdk.middleware import fastapi_wrap, flask_middleware, DjangoMiddleware

# FastAPI：wrap 原 app
app = fastapi_wrap(app, resolver=lambda req: "mindspore" if req.url.path.startswith("/mindspore") else None)

# Flask
flask_middleware(app, resolver=lambda req: "openeuler")   # 单社区也可直接给常量

# Django（MIDDLEWARE 加 DjangoMiddleware，子类里可覆写 resolve_community）
MIDDLEWARE = [..., "obs_sdk.middleware.DjangoMiddleware"]
```

中间件会注入 `request_id`（沿用 `X-Request-Id` 或生成）并 push 请求上下文；请求内日志 / 指标自动带覆盖值，
处理结束上下文还原。

同时记 HTTP 服务端指标（spec/metrics-format.md）：

| 指标 | label |
| --- | --- |
| `http_server_requests_total` | `method` `path` `status_code` |
| `http_server_request_duration_seconds` | 同上（直方图，桶边界与 Go / Node 对齐） |

`path` 取**路由模板**（`/items/{item_id}`），不是原始 URL —— 原始路径带 ID 会撑爆时序基数；
404 / 未匹配归到 `unmatched`。`service/env/instance/community` 由 SDK 自动补齐。

服务已有等价 instrumentation（如自挂 prometheus-fastapi-instrumentator）时，传
`collect_server_metrics=False` 关掉，避免同一指标被两处记录（`DjangoMiddleware` 则在子类里把
`collect_server_metrics` 置 `False`）。

## 验证

```bash
pip install -e '.[test]'    # 在 python/ 下；[test] 已含 pytest + 三个 Web 框架
pytest
```

框架用 `pytest.importorskip` 跳过 —— 不装就会「静默跳过」，本地看着全绿而中间件实际零覆盖，
所以 `[test]` 里把 fastapi / flask / django 一起钉上了。
