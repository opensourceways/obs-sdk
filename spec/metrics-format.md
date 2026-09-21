# 指标格式契约 — 命名与 label 规范

> 采集链路：服务暴露 `/metrics`（Prometheus text 格式）→ 各集群 **Prometheus Agent**（`kubernetes_sd_configs` 直抓，**不依赖 ServiceMonitor CRD**）→ `remote_write` 到**中心 Prometheus** → 大盘/告警。格式统一是跨服务聚合的前提。
>
> ⚠️ 抓取侧**必须开 `honor_labels: true`**，否则本文件定义的通用 label 会被 Prometheus 的服务端 label 顶掉 —— 见「抓取侧的 label 冲突」。

## 指标命名前缀

Prometheus 指标命名规则基础上追加组织约束：

- 指标名全小写，`snake_case`，单位后缀遵循 Prometheus 约定（`_total` / `_seconds` / `_bytes` / `_count` / `_sum`）。
- **业务指标强制前缀**：`<service>_`（service 短名，服务唯一）。例：`robot-universal-review_http_requests_total` → 简写 `review_http_requests_total`。
  - 也可按服务内子系统细分：`<service>_<subsystem>_<name>_<unit>`。
- **中间件公共指标不加任何前缀**，直接用 `http_server_*` 这类通用名（见下节表）。既不加 `<service>_`，也不加 SDK 保留前缀（如 `obs_`）：
  - 同一条时间序列已带 `service` label，查询按 label 过滤 —— 名字里再编一遍前缀是重复信息；
  - 带上前缀会让「同一指标名下的跨服务查询」失效，每个服务得各写一套名字；
  - Actuator / Micrometer 等**官方 instrumentation 的默认名不带这类前缀**（如 `http_server_requests_seconds`），自造前缀需要额外做一层名字映射，与「薄封装、不自研 instrumentation」冲突。

## 通用 label 集（每个时间序列必须带）

SDK 统一为所有注册的指标自动附加以下 **const label**（值来自 Init 注入的静态字段）：

| label | 值来源 | 说明 |
| --- | --- | --- |
| `service` | Init | 服务名 |
| `env` | Init | 环境 |
| `instance` | Init | 实例标识 |
| `community` | Init 默认 **+ 请求级覆盖** | 见下节——唯一一个可动态的公共 label |

> 其余业务维度 label（`endpoint` / `method` / `status` / `code` 等）由业务/中间件按需声明。
> **禁止**把 `request_id` / `trace_id` / `span_id` 等高基数值当 label——会撑爆时序基数。

## community 双层注入在指标上的实现

同一个注册、两套取值来源：

- **静态默认**：初始化时把默认 community 作为 const label 打进注册表。单社区独立部署服务到此为止，全部时间序列带固定 `community`。
- **动态覆盖**：中心化服务需要区分社区时——SDK 指标 API 的**打点函数接受可选 community 覆盖**（与日志绑定式/参数式一致）：
  - 实现方式（推荐，兼容官方库）：对需要区分 community 的业务指标，注册时把 `community` 声明为**普通可变 label**（同时保留 service/env/instance 作 const label），打点时由 SDK 从上下文/覆盖参数取出 community 值填 label。
  - 即：**同一条指标**，单社区场景填默认值即可；多社区场景填覆盖值。注册一次，两用。
- 若官方库只支持 const label（无法按请求覆盖），SDK 应暴露「注册时声明该指标 community 是否动态」的两套方法，内部按官方库能力映射到 const label 或普通 label。

## 抓取侧的 label 冲突（Agent / 抓取配置必读）

上面那四个通用 label 是**服务自己暴露出来的**（scraped label）。Prometheus 抓取时还会附加一批
**服务端 label**：`job`、`instance`（**默认值就是抓取地址 `__address__`**），以及
`kubernetes_sd_configs` / `relabel_configs` 打上的 label。**两者同名即冲突。**

冲突时的行为由 job 上的 [`honor_labels`](https://prometheus.io/docs/prometheus/latest/configuration/configuration/) 决定：

| `honor_labels` | 结果 |
| --- | --- |
| `false`（**Prometheus 默认**） | **服务端 label 获胜**，服务暴露的那个被改名成 `exported_<原名>` |
| `true` | **服务暴露的获胜**，冲突的那个服务端 label 被丢弃 |

> ⚠️ **规则：所有抓取本 SDK 输出指标的 job，一律 `honor_labels: true`。**

不开的话，`instance` 是**必然**会被顶掉的一个 —— 不需要写任何 relabel，Prometheus 天生就会把
`__address__` 塞进 `instance`，于是：

- 契约要求的 `instance` 变成抓取地址，SDK 打的部署实例名被挤到 `exported_instance`；
- **日志里的 `instance` 是部署实例名，指标里的 `instance` 是 pod IP —— 同一个字段名两套含义，
  日志与指标再也无法用 `instance` 关联**；
- 按本契约写的查询/告警会**静默查空**（`exported_instance` 这个键不在契约里，不报错、只是没数）。

实测输出（`prometheus 3.11.3`，同一靶子、两个 job 只差 `honor_labels` 一行）：

```
# honor_labels: false（默认）—— SDK 打的 instance 被顶掉
http_server_requests_total{instance="127.0.0.1:18081", exported_instance="pod-1", service="review", …}

# honor_labels: true —— 契约字段保住
http_server_requests_total{instance="pod-1", service="review", …}
```

### 三条配套纪律

1. **`honor_labels` 是逐 label 生效的**，不是全有全无 —— **不冲突**的服务端 label 会原样保留。
   所以「开 `honor_labels: true`」与「用 SD / relabel 打 `namespace` / `pod` 等 SDK 不输出的
   label」**可以并存**，这也是推荐的组合：`instance` 归 SDK，`namespace` / `pod` 归抓取侧。
2. **`instance` 已被 SDK 占用。** 抓取侧若要保留目标地址（排查「这条数来自哪个 pod」很有用），
   **另起一个 label**（如 `pod_ip`），不要占用 `instance` —— `honor_labels: true` 下两者无法共存。
3. **不要把 `__meta_kubernetes_service_name` 直接映射成 `service`** —— 与 SDK 的 `service` 正面冲突。
   需要的话起名 `k8s_service` 之类。

> **同一条规则对整个 Kubernetes 生态的 exporter 都成立**（例如 kube-state-metrics 自带
> `namespace` / `pod`，那是**被监控对象**的维度）。Agent 侧**所有 job 统一开 `honor_labels: true`**，
> 不要按 job 分别判断。实测踩过的坑：KSM job 上打过同名 target label，结果 83 个 Pod 的
> `kube_pod_info` 全被贴成 `namespace="monitoring"`、真实命名空间被挤到 `exported_namespace`，
> 任何 `kube_pod_info{namespace="review"}` 都查不到数（见 infra-common#4272）。

> **写入瘦身的禁忌**：`write_relabel_configs` 里做 `labeldrop` 时，**不要 drop
> `service` / `env` / `instance` / `community`** —— 这四个是跨语言、跨集群查询的锚点，drop 掉之后
> 该集群的数据在中心大盘上直接归不了类。该 drop 的是 `container` / `container_id` / `uid` 这类。

## 默认暴露的中间件指标（可选，按服务依赖挑）

| 指标 | 类型 | label |
| --- | --- | --- |
| `http_server_requests_total` | Counter | service, env, instance, community, method, path(可选,注意基数), status_code |
| `http_server_request_duration_seconds` | Histogram | service, env, instance, community, method, path(可选), status_code |
| `log_entries_total` | Counter | service, env, instance, community, level |

> 不强制：服务只要保证**自己注册的业务指标**带通用 label 即可；中间件公共指标（若启用）用上述通用名，
> 且**路径不入 label 或按低基数路由模板入 label**（`/items/{id}`，不是 `/items/123`）。
>
> `status_code` 在 counter 与 histogram 上**都带**：按状态码看时延（如只看 5xx 的 P99）是常见诉求，
> 只在 counter 上带会让这个查询无法表达。
>
> 同一指标在**各语言 SDK 间名字必须一致**；Java 走 Actuator 时 Micrometer 的默认名为
> `http_server_requests_seconds`（label 用 `status` / `uri`，无 `_total`），与上表不同 —— 这是官方
> instrumentation 的既定形状，SDK 不做名字映射（见「服务要求」的多语言同构边界）。

## 服务要求

- 每个服务暴露一个统一 `/metrics` HTTP 端点（Prometheus text exposition）。
- 抓取协议：HTTP `GET /metrics`，官方库默认 behavior，content-type `text/plain; version=0.0.4`。
- 多语言同构：四个语言 SDK 都要能输出**语义等价**的 `service/env/instance/community` 组合，供各集群 Agent 统一抓取（`kubernetes_sd_configs`，不依赖 ServiceMonitor CRD）、中心 Prometheus 统一查询。
  - 同构的**边界**：`service/env/instance/community` 这套通用 label 与「不重复埋点」的官方 instrumentation 是强约束；
    服务端指标的**名字与其余 label** 则由各语言官方库的既定形状决定，SDK 只做装配、不改名。
    因此 Java（Actuator + Micrometer）的 `http_server_requests_seconds` / `status` / `uri` 与
    Go / Python / Node 的 `http_server_requests_total` / `status_code` / `path` 并不逐字相同。
    跨语言统一查询请对齐**通用 label**，不要把服务端指标名写进跨语言的告警规则。
