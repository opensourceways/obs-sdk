# CI workflow 可观测一期执行清单

> 一期组件：`ci-workflow-aggregator`。为什么这么设计、指标怎么定义、告警怎么分级，见
> [CI workflow 可观测技术设计](CI%20workflow可观测技术设计.md)；本文只做**落地执行清单**，可逐条勾选。
>
> 状态图例：`[ ]` 待办 ｜ `[x]` 完成。
> 跟踪方式：`project:ci-observability` label 与服务注册都还没做（见第 2 节），所以暂时以本文作为一期跟踪入口，
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
- [x] **生产 PAT 形态**（已定）：fine-grained，权限 `Actions:read` + `Contents:read` + `Metadata:read`，预算 2000–2250 次/小时（45%）。
- [x] **GitCode Actions API 实测**（2026-10-28 匿名实测 `Ascend/pytorch`，结论如下）：

  | 项 | 实测结论 |
  | --- | --- |
  | base path | **`https://api.gitcode.com/api/v8/repos/{owner}/{repo}/actions/*`**。`gitcode.com/api/v8/...`（无 `api.` 前缀）返回 `404 NOT_PATH`；`api.gitcode.com/api/v8/{不存在的路由}` 返回 text/plain `404 Route Not Found`，**可与业务 404 区分** |
  | 匿名可读 | `actions/runs`、`actions/workflows`、`runs/{id}/jobs` **全部 200，无需 token**；不存在的仓返回 `{"error_code":404,"error_code_name":"UN_KNOW","error_message":"Project not found:..."}`。⚠️ v5 侧的「401」是**未匹配子路径的兜底**（`/repos/{o}/{r}/nonexistent-xyz` 也 401），**不能拿 401 判端点存在** |
  | 分页/过滤 | 列表 `{total_count, workflow_runs[]}`；`per_page` **上限 100**（传 200 仍回 100）；按 `start_time` **严格倒序**；服务端过滤 `status` / `branch` / `workflow_id` / `page`；`since` **被忽略**（无时间过滤） |
  | 状态取值 | run：`RUNNING` / `COMPLETED`（成功） / `FAILED` / `CANCELED`（job：`COMPLETED` / `FAILED` / `IGNORED`=跳过）。**没有 `QUEUED`**（在途只有 `RUNNING`），**也没观测到 `FAILED(...)` 括号原因** |
  | 时间单位 | 全部**毫秒**：`start_time` / `end_time` / job 的 `apply_time` / `execute_cost_time`（如 38000 = 38s） |
  | 关键字段 | run：`workflow_run_id`（32 位 hex 字符串）、`file_path`（`.gitcode/workflows/x.yml`，**作 workflow 身份**）、`event`（实测 `Note`=评论触发、`MR`）、`pull_request_id`、`head_branch`、`head_sha`、`run_number`、`attempt`（**仅详情有**）、`exist_in_default_branch` |
  | job 结构 | `runs/{id}/jobs` 返回**扁平 `jobs[]`**（`total_count` 71 全量一次给），含 `apply_time`→`start_time` 可算排队；`resource` 是 **JSON 字符串**；`stages[]` 只出现在 360KB 的 run 详情里 |
  | 详情接口 | `runs/{id}` 单次 **360KB 左右** → **不要轮询**，只在需要 `attempt` 等字段时取 |
  | 限流 | 响应**不带任何 `x-ratelimit-*` 头**（只有 `trace-id`、`total_count`、`total_page`）→ 余量只能靠 429 计数或压测标定 |

  与设计文档的三处矛盾（待同步改设计，见回复）：job 是否嵌套、`FAILED(...)` 解析、`event` 取值映射。
- [x] **preview / test 是否可挂 PVC**（2026-10-28 实测，结论：**都能挂，按 PVC 走**）：

  | 集群 | 结论 | 依据 |
  | --- | --- | --- |
  | `infra-hk-test-cluster-001`（test） | ✅ 可挂 | `default` ns 已有 6 个 Bound PVC（`csi-disk-dss`，127d）；SA 有 `create persistentvolumeclaims`；**新建 1Gi `csi-disk` RWO，10–20s Bound**，验证后已删除 |
  | `infra-hk-preview-cluster-003`（preview） | ✅ 可挂 | cluster-admin；SC 齐全（`csi-disk` / `csi-disk-topology` / `csi-disk-dss` / `csi-local*` / `csi-nas` / `csi-obs`）；**新建 1Gi `csi-disk` RWO，10–20s Bound**，验证后已删除 |

  ⚠️ **两个集群都没有默认 StorageClass**（`storageclass.kubernetes.io/is-default-class` 为空）→ chart 必须**显式写 `storageClassName: csi-disk`**，漏写会永远 Pending（不是报错，是静默卡住）。`csi-disk` 是 `Immediate` 绑定；若节点跨可用区，改用 `csi-disk-topology`（`WaitForFirstConsumer`）更稳。

  验证是在 `default` ns 做的：正式部署 ns 由 ai-workflow / ArgoCD 新建，可能叠加 ResourceQuota（当前 SA 无 cluster-scope 读权限，查不到）。上线时以首次 sync 的 PVC 状态为准，仍保留「PVC Pending 超过 5 分钟 → 降级 `emptyDir` + 重启不回填」的兜底开关。
- [x] **失败口径单一实现**（已定）：在**归一后的 conclusion 枚举**上实现唯一一个函数，告警（L1/L2/L2′/L4）、连续失败、大盘共用；平台原值先映射、再判定，不在查询里各写各的。

  | 归类 | GitHub 原值 | GitCode 原值 |
  | --- | --- | --- |
  | **计失败** | `failure` `timed_out` `startup_failure` | `FAILED` |
  | 不计失败、**进分母** | `success` `cancelled` `skipped` `neutral` `stale` | `COMPLETED` `CANCELED` `IGNORED` |
  | 不计失败、**不进分母** | `action_required`（等人工批准，从未执行） | —（无对应取值） |
  | **未知取值** | 原样透传：不算失败，但**留在分母**，靠 `conclusion` label 原值暴露 | 同左 |

  未知取值留在分母而不是剔除：剔除会让平台新增取值时分母静默变小、失败率虚高；留下则不改变比率，且新取值会以大字段原值出现在大盘上，能被发现。`action_required` 是唯一例外——它从未执行，计入会稀释失败率。
- [x] **workflow 排除清单初版**（已定）：走 values 的 `exclude_workflows[]` **配置下发，不写死在代码里**；按归一后的 `workflow` 身份**精确匹配**（GitHub 取 `run.path` 文件名去扩展名，GitCode 取 `file_path` 文件名去扩展名，如 `.gitcode/workflows/PR-pipeline_pytorch.yml` → `PR-pipeline_pytorch`），**不做前缀/正则模糊匹配**——模糊匹配在 workflow 改名后会静默误伤。剔除点定在**归一化之后、状态层之前**：不进 state、不进任何 `ci_workflow_*` 序列，因此也不占后续轮询配额。

  初版四类（量级依据见设计 §3.1 与 §4.1 的实测）：

  | 类别 | 例 | 为什么排除 |
  | --- | --- | --- |
  | 标签 / 评论 / 欢迎机器人 | `community-labels`、`stale_manage`、`Ready Label`、`pr-title-check` | 失败多半是 token 过期，不代表 CI 健康；实测 `community-labels` 占 vllm-project L2 原始 30 趟里的 10 趟 |
  | 安全扫描 | scorecard、trivy、codeql 类 | 红常由规则库更新或上游驱动，不是代码回归 |
  | 发布构建 | 带 `release` / `publish` / `image` 语义的流水线 | 发布失败另有处理链路，混进门禁会让 L2 指向错误的处理人 |
  | 纯文档 / 格式 | doc-only 的 lint / format workflow（可选，按仓逐个确认） | 与构建健康度无关，且极易被 bot 触发 |

  清单随仓库清单一起演进：新增 workflow 默认**采集**，排除必须显式加配置——宁可多看几条，不要因默认排除漏掉真回归。

## 1. 工程骨架

- [ ] 建仓 `opensourceways/ci-workflow-aggregator`：Go module（对齐 obs-sdk 的 go 版本）、`cmd/aggregator`、`internal/{config,source,state,scheduler,metrics}`、多阶段 Dockerfile（非 root）、CI。
- [ ] 建 umbrella 仓 **`opensourceways/ci-observability`**（CI 可观测）：`.gitmodules` 挂 `ci-workflow-aggregator` 与 `jenkins-exporter` 两个子仓 + README（写清「本仓不含 obs-sdk，装的是 CI 侧采集器」）。**submodule 只能指向仓库根**，所以两个子仓必须各自独立成仓，不能把 exporter 塞进 `ci-all` 的子目录。
- [ ] 依赖 `github.com/opensourceways/obs-sdk/go@go/v1.1.0`，不引第三方 prometheus 客户端。
- [ ] chart：Deployment **单副本**、containerPort 8080 **命名 `http`**、Service 端口同名、`/healthz` 探针、PVC 1Gi `csi-disk` RWO + `strategy: Recreate`（单副本前提，滚动升级会撞 EVS 多挂）。
- [ ] values 环境化：`repos[]`（platform / owner/name / community）、轮询间隔、token Secret 名、state 路径、日志级别、workflow 排除清单。
- [ ] 仓库清单与 `ascend-ci-deployment/projects/<org>/<repo>/values.yaml` 做一次 drift 校验（新增仓不会静默漏采）。

## 2. ai-workflow 接入

- [ ] backlog 提 PR 加 **`.ai-flow/services/ci-observability.yaml`**（**一个服务覆盖两个子仓**）：
  - `service.id: ci-observability`、`service.label: project:ci-observability`、`implement.tools_repo: opensourceways/ci-observability`（子仓是它的 submodule，引擎按 changed submodule 决定构建谁）
  - `preview`：hk-003 + 独立 ns `ci-observability`，**不需要 ingress**（两个组件都没有外部入口，只有 `/metrics`）
  - `release.deploy: argocd`；**两个子仓各一个部署子目录 / 各一个 ArgoCD Application**（`ci-observability/aggregator/`、`ci-observability/exporter/`），`format: kustomize` + 逐行 `image_names` 把子仓名映射到各自 `images[].name`。引擎的 kustomize 归档本来就是**按 sub 各自找 kustomization**（apply_tag_sync.py 注释明说「每个 sub 可能有独立的 kustomization.yaml 在不同子目录下」），塞进同一份 kustomization 是没被人走过的那条路
  - `argocd.pod_selector` 用**两个 Deployment 共打的** `app.kubernetes.io/part-of=ci-observability`——只匹配单个组件的话，另一个组件的镜像替换不会被校验，会「绿着但没部署」
  - 同一 ns `ci-observability`，但 **Deployment 分开、两块 PVC 分开**：两个子仓都是**常驻微服务**（各自 Deployment / Service / 凭据 Secret / 自监控 / 健康探针），「exporter」只是**职责命名**（对外部系统做指标适配），不是部署形态上的简化。两者都单副本 + `strategy: Recreate` + 1Gi PVC：aggregator 存 `state.json`，exporter 存 **build number 水位线**（不持久化则每次重启把最近 N 条 build 重数一遍，`jenkins_build_result_total` 虚高——见接入指南 §6.1.2）。**不能共用一块 PVC**（RWO 要求两个 Pod 落同一节点，是运气不是设计）
  - **infra-common/service.md 每个子仓每环境各留一行**（镜像名 / 归档子路径 / 镜像版本路径 / 归档方式）：镜像路径与归档目标是这张表的「单一事实来源」，缺行 ⇒ 该子仓**不构建也不归档，且不报错**
  - **每个子仓根目录必须有 Dockerfile**：构建循环是 `[ -f "$smpath/Dockerfile" ] || continue`，没有就静默跳过；镜像仓名取**子仓名小写**
  - ⚠️ 引擎用的是 `git submodule update --init --recursive --remote`：**构建跟子仓分支头，不跟 umbrella 钉的 SHA**，umbrella 的 commit 不是发布快照，回滚按镜像 tag 走；子仓合入**不需要**回 umbrella 提指针 PR
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
- [ ] **jenkins-exporter 的额外硬约束（同一 umbrella，不同环境要求）**：它必须能访问 `openeulerjenkins.osinfra.cn`（[接入指南 §6.1.5](../docs/可观测接入指南.md)——**该域名在部分出口解析不到**）。所以三环境上线前先实测 DNS + HTTPS：解析不到 / 连不通的环境只能验收「进程起来 + `/metrics` 暴露 + 自监控三项」，**不能**验收真实采集，否则就是「上线即空跑」还绿着。若 preview / test 都不通，exporter 的联调只在 prod 做，B 段预览退化为镜像构建验证。

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
