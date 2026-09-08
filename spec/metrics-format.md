# 指标格式契约 — 命名与 label 规范

> 采集链路：服务暴露 `/metrics`（Prometheus text 格式）→ AOM 2.0 Prometheus 实例（ServiceMonitor 抓取）→ 大盘/告警。格式统一是跨服务聚合的前提。

## 指标命名前缀

Prometheus 指标命名规则基础上追加组织约束：

- 指标名全小写，`snake_case`，单位后缀遵循 Prometheus 约定（`_total` / `_seconds` / `_bytes` / `_count` / `_sum`）。
- **强制前缀**：`<service>_`（service 短名，服务唯一）。例：`robot-universal-review_http_requests_total` → 简写 `review_http_requests_total`。
  - 也可按服务内子系统细分：`<service>_<subsystem>_<name>_<unit>`。
- 若部署跨多个服务的共享 SDK 中间件统一暴露公共指标，则用 SDK 保留前缀 `obs_`（见 middleware 指标），避免与服务业务指标混名。例：`obs_http_server_requests_total`、`obs_http_server_request_duration_seconds`。
  - 中间件指标前缀统一 `obs_`，**不以 service 名开头**，因为同一条时间序列已带 `service` label；查询按 label 过滤。

## 通用 label 集（每个时间序列必须带）

SDK 统一为所有注册的指标自动附加以下 **const label**（值来自 Init 注入的静态字段）：

| label | 值来源 | 说明 |
| --- | --- | --- |
| `service` | Init | 服务名 |
| `env` | Init | 环境 |
| `instance` | Init | 实例标识 |
| `community` | Init 默认 **+ 请求级覆盖** | 见下节——唯一一个可动态的公共 label |

> 其余业务维度 label（`endpoint` / `method` / `status` / `code` 等）由业务/中间件按需声明。
> **禁止**把 `request_id` 等高基数值当 label——会撑爆时序基数。

## community 双层注入在指标上的实现

同一个注册、两套取值来源：

- **静态默认**：初始化时把默认 community 作为 const label 打进注册表。单社区独立部署服务到此为止，全部时间序列带固定 `community`。
- **动态覆盖**：中心化服务需要区分社区时——SDK 指标 API 的**打点函数接受可选 community 覆盖**（与日志绑定式/参数式一致）：
  - 实现方式（推荐，兼容官方库）：对需要区分 community 的业务指标，注册时把 `community` 声明为**普通可变 label**（同时保留 service/env/instance 作 const label），打点时由 SDK 从上下文/覆盖参数取出 community 值填 label。
  - 即：**同一条指标**，单社区场景填默认值即可；多社区场景填覆盖值。注册一次，两用。
- 若官方库只支持 const label（无法按请求覆盖），SDK 应暴露「注册时声明该指标 community 是否动态」的两套方法，内部按官方库能力映射到 const label 或普通 label。

## 默认暴露的中间件指标（可选，按服务依赖挑）

| 指标 | 类型 | label |
| --- | --- | --- |
| `obs_http_server_requests_total` | Counter | service, env, instance, community, method, path(可选,注意基数), status_code |
| `obs_http_server_request_duration_seconds` | Histogram | service, env, instance, community, method, path(可选) |
| `obs_log_entries_total` | Counter | service, env, instance, community, level |

> 不强制：服务只要保证**自己注册的业务指标**带通用 label 即可；中间件公共指标（若启用）用 `obs_` 前缀，且**路径不入 label 或按低基数路由模板入 label**。

## 服务要求

- 每个服务暴露一个统一 `/metrics` HTTP 端点（Prometheus text exposition）。
- 抓取协议：HTTP `GET /metrics`，官方库默认 behavior，content-type `text/plain; version=0.0.4`。
- 多语言同构：四个语言 SDK 都要能输出**语义等价**的 `service/env/instance/community` 组合，供 ServiceMonitor 统一抓取、AOM/Cortex 统一查询。
