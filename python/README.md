# obs-sdk-python

opensourceways 微服务可观测薄封装 SDK 的 Python 实现，契约见 [spec/](../spec/README.md)。

- **日志**：`obs_sdk.log` —— 结构化 JSON（root logger 挂唯一 JsonHandler，字段规范见 spec/log-format.md）
- **指标**：`obs_sdk.metrics` —— prometheus-client 薄封装，自带独立 CollectorRegistry
- **请求上下文**：`obs_sdk._context` —— `contextvars` 承载 `community/request_id/trace_id/span_id`（后两者为二期 trace 预留位）
- **框架适配**：`obs_sdk.middleware` —— FastAPI / Flask / Django 中间件（注入 request_id + 可信判定点解析 community）
- **community 双层注入**：`service/env/instance` 常驻 const；`community` 可变 —— 请求上下文覆盖（`_context.bind`），未覆盖回退部署默认（`OBS_*` 环境变量）

## 日志用法

```python
import logging
from obs_sdk import log

# 字段空则回退 OBS_SERVICE / OBS_ENV / OBS_INSTANCE / OBS_COMMUNITY；进程内幂等
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

`metrics.init()` 默认单例读 `OBS_*` 环境变量；多注册表场景直接 `Metrics(...)`。

## 请求上下文 / 中间件（community 双层注入）

请求级 community 只在**服务可信判定点**（路由前缀 / 认证主体 / 白名单）解析后显式 bind，
不从不加鉴别的 URL / Header 盲取（见 spec/community-values.md）。

```python
from obs_sdk.middleware import fastapi_wrap, flask_middleware, DjangoMiddleware

# FastAPI：wrap 原 app
app = fastapi_wrap(app, resolver=lambda req: "mindspore" if req.url.path.startswith("/mindspore") else None)

# Flask
flask_middleware(app, resolver=lambda: "openeuler")   # 单社区可不传 resolver

# Django（MIDDLEWARE 加 ObsMiddleware，子类里可覆写 resolve_community）
MIDDLEWARE = [..., "obs_sdk.middleware.DjangoMiddleware"]
```

中间件会注入 `request_id`（沿用 `X-Request-Id` 或生成）并 push 请求上下文；请求内日志 / 指标自动带覆盖值，
处理结束上下文还原。

## 验证

```bash
pip install -e "python[test,fastapi,flask,django]"   # 或 virtualenv 装 obs_sdk + 框架
cd python && pytest
```
