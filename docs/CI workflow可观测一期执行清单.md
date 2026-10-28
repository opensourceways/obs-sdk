# CI workflow 可观测一期执行清单

> 一期组件：`ci-workflow-aggregator`。为什么这么设计、指标怎么定义、告警怎么分级，见
> [CI workflow 可观测技术设计](CI%20workflow可观测技术设计.md)；本文只做**落地执行清单**，可逐条勾选。
>
> 状态图例：`[ ]` 待办 ｜ `[x]` 完成。
> 跟踪方式：`project:ci-workflow-aggregator` label 与服务注册都还没做（见第 2 节），所以暂时以本文作为一期跟踪入口，
> 待服务注册落地后再补 backlog 跟踪 issue。
> **带 🔒 的是已实测锁定、不再讨论但要照着做的项。**

## A. 已定事实

| 环境 | 集群 | 仓库清单 | 凭据 | 指标去向 | 上线链路 |
| --- | --- | --- | --- | --- | --- |
| dev/预览 | `infra-hk-preview-cluster-003` | 1–2 仓 | 独立 token | **没有 Prometheus，只有 curl** | ai-workflow B 段 |
| test | `infra-hk-test-cluster-001` | 3–5 仓 | 独立 token | 社区中心（`monitoring.osinfra.cn` / ns `infra-monitoring-community`） | ai-workflow C 段（argocd） |
| prod | `infra-cn4-x86-common-cluster` | 21 仓 + GitCode | 生产 PAT | CI 中心（`113.44.182.82` / ns `infra-monitoring`） | release-mgmt 变更计划书 |

🔒 出网已验证（三个集群实测）：`api.github.com` 与 `api.gitcode.com` 均可直连，无需代理/白名单，0.1–0.6s。GitHub 匿名配额实测 60/h → **生产 PAT 是硬需求**。
🔒 test 的采集靠社区 agent 的 `services` job **自动发现**，前提是 **Service 端口名必须是 `http` / `http-port` / `srv` / `tls-1` / `ether-http`**。叫 `metrics` 就静默不采集。
🔒 prod 侧 CI 中心只有固定 job，**必须手工加 scrape job**，且 `honor_labels: true`。
🔒 cn4 可挂 PVC（`storageClassName: csi-disk`，中心 Prometheus 在用）。

## 0. 前置决策（不做完就别写业务代码）

- [x] **spec 指标前缀条款**（已落）：设计钉的是 `ci_workflow_*`，[spec/metrics-format.md:12](../spec/metrics-format.md#L12) 要求业务指标 `<service>_`。已在 spec 补「领域共享指标」条款与[已登记的领域前缀](../spec/metrics-format.md#已登记的领域前缀)表，`ci_workflow_` 登记在案；前提是**同一事实同名同 schema + 单点入账**（事件路径推给 aggregator 合并，不各自计数）。
- [x] **`community` 取值**（已定）：**固定 `OBS_COMMUNITY=common`**，不做请求级覆盖。观测对象是 `vllm-project` / `triton-lang` / GitCode 组织，都不在 [spec/community-values.md](../spec/community-values.md) 枚举内；跨仓区分走 `repo` label。
- [ ] **GitCode Actions API 实测（阻塞适配器，最高优先级）**：集群内实测 `/api/v8/...` 全 404、`/api/v5/repos/{o}/{r}` 返回 401、`/api/v5/repos/{o}/{r}/issues` 200。设计文档里的 v8 与本地资料（v8 = 企业/角色/邀请）矛盾。要定清：真实 base path、哪些端点匿名可读、token header 形式、时间字段单位、限流返回头。
- [ ] **生产 PAT 形态**：fine-grained，权限 `Actions:read` + `Contents:read` + `Metadata:read`，预算 2000–2250 次/小时（45%）。
- [ ] **preview / test 是否可挂 PVC**：不可挂就让状态落 `emptyDir`、允许「重启不回填」（一期语义允许）。
- [ ] **失败口径单一实现**：`failure|timed_out|startup_failure` 计失败；`action_required` 不进分母。告警、连续失败、大盘共用同一函数。
- [ ] **workflow 排除清单初版**（标签/评论机器人、scorecard/trivy、发布构建类），随配置下发不写死。

## 1. 工程骨架

- [ ] 建仓 `opensourceways/ci-workflow-aggregator`：Go module（对齐 obs-sdk 的 go 版本）、`cmd/aggregator`、`internal/{config,source,state,scheduler,metrics}`、多阶段 Dockerfile（非 root）、CI。
- [ ] 依赖 `github.com/opensourceways/obs-sdk/go@go/v1.1.0`，不引第三方 prometheus 客户端。
- [ ] chart：Deployment **单副本**、containerPort 8080 **命名 `http`**、Service 端口同名、`/healthz` 探针、PVC 1Gi `csi-disk` RWO + `strategy: Recreate`（单副本前提，滚动升级会撞 EVS 多挂）。
- [ ] values 环境化：`repos[]`（platform / owner/name / community）、轮询间隔、token Secret 名、state 路径、日志级别、workflow 排除清单。
- [ ] 仓库清单与 `ascend-ci-deployment/projects/<org>/<repo>/values.yaml` 做一次 drift 校验（新增仓不会静默漏采）。

## 2. ai-workflow 接入

- [ ] backlog 提 PR 加 `.ai-flow/services/ci-workflow-aggregator.yaml`：`service.label: project:ci-workflow-aggregator`、`implement.tools_repo`、`preview`（hk-003 + 独立 ns，**不需要 ingress**）、`release.deploy: argocd` + `argocd.cluster/namespace`。
- [ ] 三环境凭据走 Vault → Secret（各自 path，**不进 git**）。
- [ ] `argocd-application` 仓建 test 集群 Application：`project: <同集群>` + **`destination.name: in-cluster`**（写集群名会静默不同步）。
- [ ] 建 issue（打 `project:` label）驱动 A/B/C 三段，别手工绕开流水线。
- [ ] prod 走 release-mgmt 变更计划书，核对 values 仓确实是集群读的那个（`sources[].repoURL` / `ref: values`）与 `targetRevision` 不低于 chart 门槛。

## 3. 核心（平台无关，先于适配器）

- [ ] `Source` 接口定稿：`ListRuns(status, since, page)` / `GetRun(id)` / `ListJobs(id)`，归一模型 `Run` / `Job`（含 `OrderKey`）。**一次到位**，晚加等于全部序列换名。
- [ ] 状态层：去重键 `<platform>:<repo>:<run_id>:<attempt>`、在途 registry、每仓统计。
- [ ] 三条轮询：A 60s（completed 首页）、B 5min（queued + in_progress）、B+ 5min（在途 run 的 `/jobs`）。
- [ ] 终态判定：列表消失 + **本轮两条请求都成功** → `GetRun` 核实 → 入账时**补抓一次 `/jobs`**（不补就系统性丢掉撞 `timeout-minutes` 的 job）。
- [ ] 剪枝两条同时满足：`order_key < 本轮 A 首页最老` **且** `now − processed_at > 2h`。
- [ ] 重启基线：首页整体写入、**不回填**。
- [ ] `state.json` 原子写（tmp + fsync + rename，30s 或 200 条刷一次）；**必须持久化 `consecutive_failures` 与 `last_success`**——这两个不能从 counter 反推，丢了 L2/L3 直接失明。
- [ ] 四个守卫：列表截断、首页溢出、48h 未收敛、`runs_finalized_total{source}` 长期为 0。
- [ ] 单仓超时 + 单仓失败降级：保留上一轮值，只加 `api_errors_total`，不拖垮整轮。

## 4. 两个适配器

**GitHub**

- [ ] `status` + `conclusion` 双字段；`conclusion` 原值透传，未知值原样保留，**不得兜底成 failure/success**。
- [ ] `OrderKey = created_at`；`workflow` 取 `run.path` 文件名去扩展名（**不取 `run.name`**）；`branch_class` 用缓存的默认分支名。
- [ ] `status=completed` 是分组语义；截断判据用 `per_page` 不用 `total_count`。
- [ ] 401/403/429/超时分类 + `X-RateLimit-Remaining` 配额余量。

**GitCode**（`Source` 定稿后并行，允许一期后段交付；**不许先只做 GitHub 再回头改契约**）

- [ ] 按第 0 节的实测结论定 base path 与鉴权（Bearer vs 其他 header；**不用 `access_token` 查询参数**，会进访问日志）。
- [ ] 单 `status` 拆成阶段 + 结论：`COMPLETED→success`、`FAILED(...)→failure`（`split("(")[0]`）、`CANCELED→cancelled`、`IGNORED→skipped`、`PAUSED`/`SUPPEND` 保留独立取值。
- [ ] `OrderKey = start_time`（无 `created_at`）；毫秒时间戳换算。
- [ ] `stages[].jobs[]` 展平；排队 = `start_time − apply_time`，`apply_time` 为 null 跳过该样本。
- [ ] 实测「长 run 是否因新 run 创建而变老」，决定是否照搬 GitHub 的补齐逻辑。

## 5. 指标与自监控

- [ ] 落地 19 个指标（7 结果/耗时 + 5 在途 + 7 自监控），名字与 label 以设计 §2 为准。
- [ ] `ci_workflow_last_success_timestamp_seconds` 从未成功时落 **0**（缺失 = L3 对新接入 workflow 完全失明）。
- [ ] 连续失败规则：只累加三类失败、`success` 归零、其余不动；`action_required` 不记 duration。
- [ ] `/metrics` 独立暴露，**不要用 obs-sdk 的 HTTP 中间件包住它**（否则自采计入 `http_server_*`）。
- [ ] 禁止标签自检：`run_id` / `job_id` / `head_sha` / `job_workflow_ref` / `status`。
- [ ] `OBS_SERVICE=ci-workflow-aggregator`、`OBS_ENV` 按环境、`OBS_COMMUNITY=common` 在 chart 里显式注入（不注入会落 `unknown`）。
- [ ] 单测用 fixture 回放两平台响应：未知枚举、null `apply_time`、毫秒时间戳、`FAILED(1)`、首页溢出、剪枝不误删、重启不重复计数。

## 6. 三环境上线（顺序不能跳）

- [ ] **dev**：1 个仓跑通 A/B/B+；验收判据是 **pod 内 `curl localhost:8080/metrics`**——hk-003 没有任何 Prometheus，别去找大盘。不建盘、不建告警。
- [ ] **test**：3–5 仓 → 社区 agent 自动抓（端口名 `http`）→ 社区中心。判据用 `scrape_samples_scraped{cluster="infra-hk-test-cluster-001"} > 0`，**不要看 `up`**（空体 200 也 up=1）。
- [ ] **prod 灰度**：先 3 仓 24h 看配额与错误率 → 全量 21 仓；同时核对 GitHub 设置页的实际调用量。

## 7. 监控接入（prod，独立 PR）

- [ ] `ascend-ci-deployment/monitoring/config-for-infra-cn4-x86-common-cluster/prometheus-scrape-config-secret.yaml` 加 job：`honor_labels: true`、relabel 出 `cluster`、`scrape_interval: 60s`。
- [ ] 同目录 `prometheus-rules.yaml` 加告警：L1 基建 / L2 门禁 / L2′ nightly / L3 静默 / L4 PR 突增 / QueueBacklog + 自监控（401/403、last_sync、unresolved）。
- [ ] 同目录 `alertmanager-config-secret.yaml` 核路由：设计 §3.1 的 `email.infra.html` 约束（`cluster` 必填、warning 黑洞、`repo/workflow/platform` 只进 description、`runbook` 必填、`group_by` 加维度）就是这一份。
- [ ] 大盘落 CI 那套 Grafana（数据在 CI 中心，社区 Grafana 看不到）；面板里别写死数据源，方便同 JSON 换源。
- [ ] 两套中心互不可见：test 的盘在社区 Grafana，prod 的盘在 CI Grafana。

## 8. 验收

- [ ] 三条链路各出样本：短 run（A）、跨 25 分钟长 run（B）、job 级（B+）。
- [ ] 冷启动：重启前后不重复计数、在途不丢、`consecutive_failures` 与 `last_success` 恢复正确。
- [ ] 故障注入：401、429、单仓超时、整页失败。
- [ ] 抽查 20 条 run 与 Actions 页面对账（不做精确对账，`total_count` 不可信）。
- [ ] 告警演练：手工造连续失败与排队时长，确认邮件字段齐全。
- [ ] 配额核算：自监控调用量 vs 实际 API 配额。

## 9. 一期只留接口、不做

- [ ] `/ingest` 占位路由（三期实现），幂等键形状已含 `<job_id>:<phase>`。
- [ ] `runs_finalized_total{source}` 的 `source` 一期恒为 `poll`。
- [ ] 二期 ghalistener 抓取、36 个 scale set 补 `listenerMetrics` 都不进一期。

---

**关键路径就两条**：第 0 节的「GitCode Actions API 实测」（阻塞适配器参数）和「spec 指标前缀条款」（阻塞所有指标落地）。其余可以并行推进；建议顺序是决策 → 骨架 + `Source` 接口 → GitHub 适配器 + dev 跑通 → GitCode 适配器 + test 跑通 → prod 灰度 + 告警大盘 → 验收。
