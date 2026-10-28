# CI workflow 可观测技术设计

> 本文是 CI workflow 观测的**技术设计**：采集架构、指标清单、分期规划与详细设计。
> 目标与边界见 [CI 可观测目标](CI可观测目标.md)，接入路线与工具使用见[可观测接入指南](可观测接入指南.md)。
> 一期落地步骤与勾选状态见 [CI workflow 可观测一期执行清单](CI%20workflow可观测一期执行清单.md)。

本文解决「CI 跑的结果怎么样、为什么慢、卡在哪」，覆盖两类业务：

- **参与型**：仓库在 GitHub，跑 GitHub Actions；
- **主导型**：仓库在 GitCode，跑 GitCode Actions。

两者共用同一套指标契约，靠 `platform` 标签（`github` / `gitcode`）区分，不做两套大盘、两套告警。

当前只有 runner 维度的在跑信息，见[CI 可观测现状 第 8 章](CI可观测现状.md)。本文是方案，尚未落地。

## 1. 整体架构

### 1.1 三个组件与两条既有通路

```
┌──────────── 每个 CI 集群（11 个） ───────────────────────────────────────────────────────────────────────┐
│                                                                                                          │
│  arc-systems: ghalistener × N（已有）                                                                    │
│     └─ :8080/metrics ──scrape──► Prometheus agent（每集群已有）                                          │
│                                                                                                          │
│  ci-event-agent（三期）：watch EphemeralRunner                                                           │
└───────────────────────┬─────────────────────────────────────────────────────┬────────────────────────────┘
                        │ 事件上报（三期）                                    │ remote_write
                        ▼                                                     ▼
┌──────────────── 中心集群 ────────────────────────────────────────────────────────────────────────────────┐
│                                                                                                          │
│  ┌──────────────────────────────────────────┐           ┌────────────────────────────────────────┐       │
│  │ ci-workflow-aggregator（单副本）         │           │ 中心 Prometheus（唯一汇聚点）          │       │
│  │  ├ Source: github/gitcode 适配器         │  /metrics │  ├ 抓取 aggregator 的 /metrics         │       │
│  │  ├ 终态轮询 A：已结束的 run（60s）       │──────────►│  └ 告警规则 ──► Alertmanager           │       │
│  │  ├ 在途轮询 B：排队 / 执行中（5min）     │           │          └─► 通知（邮件 / IM / SMN）   │       │
│  │  ├ 在途轮询 B+：在途 run 的 job（5min）  │           │               └─► 值班人               │       │
│  │  ├ 事件接收 POST /ingest（三期）         │           │                                        │       │
│  │  └ 状态: 1Gi PVC + state.json            │           │                                        │       │
│  └──────────────────────────────────────────┘           └────────────────────────────────────────┘       │
│                                                                                                          │
│               ▲ 轮询 A / B / B+                                                                          │
│               │                                                                                          │
│      GitHub Actions REST API    ·    GitCode v8 API                                                      │
└──────────────────────────────────────────────────────────────────────────────────────────────────────────┘
```

| 组件 | 位置 | 数量 | 数据源 | 产出 |
| --- | --- | --- | --- | --- |
| `ci-workflow-aggregator` | 中心集群 | **1 个**（单例） | GitHub / GitCode REST API | run/job 级结果、耗时、在途状态 |
| `ghalistener` | 各 CI 集群 `arc-systems` | 每 scale set 1 个（已有 387 个） | GitHub Actions 消息通道 | 容量 gauge、②③④ 阶段耗时（分段定义见 1.4） |
| `ci-event-agent` | 各 CI 集群 | 每集群 1 个（三期） | `EphemeralRunner` / Pod | run 身份、精确完成事件、重跑 |

**event agent 只上报 job 级事实，run 级结论只在 aggregator 产生。** 这里有**两层重复**，成因不同：

- **跨集群 agent 之间**：一个 job 只在一个集群执行，各 agent 看到的 job 互不相交，本身不重复。但如果让每个 agent 各自算 run 级结论，同一条 run 就会在它涉及的每个集群各被记一次，而且每一份都是残缺的——agent 只知道"我这个集群的这几个 job 结束了"，不知道整条 run 是否结束、结论是什么。一条 run 跨 6 个集群就会记 6 次。
- **轮询与事件之间**：同一条 run 的终态会被两条路径各发现一次，这是真重复，由 aggregator 的幂等键 `<platform>:<repo>:<run_id>:<attempt>` 合并。

### 1.2 为什么必须三种采集并存

| 观测问题 | 谁回答 | 其他源为什么答不了 |
| --- | --- | --- |
| 这次 run 成功了没有 | aggregator（轮询 API） | 集群侧没有 `conclusion`，runner 正常退出≠job 成功 |
| 哪个 job 挂了 | aggregator（`/jobs`） | 同上 |
| 现在有多少 run 在排队、排了多久 | aggregator（路径 B） | listener 只有 scale set 视角，没有 run 身份 |
| 池子够不够、扩缩容卡在哪 | ghalistener（8 个 gauge） | API 视角看不到 runner 注册数、`min/max/desired` 这些意图值 |
| 慢在 GitHub 派发还是我方扩缩容 | aggregator + ghalistener 相减 | 单看任一方只有合并值 |
| 哪个 PR、要不要重跑 | event agent（三期） | API 列表按 `created_at` 排序，重跑旧 run 落不到首页 |

### 1.3 能力矩阵（平台 × 能力）

| 能力 | GitHub | GitCode |
| --- | --- | --- |
| run/job 结果与耗时（轮询） | ✅ | ✅ 换适配器即可 |
| 在途排队/执行 gauge | ✅ | ⚠️ run 级排队状态待实测 |
| 容量 gauge、②+③ 启动耗时与 ④ 执行耗时（ghalistener） | ✅ | ❌ ARC 是 GitHub Actions 专用 |
| run 身份、重跑补齐（event agent） | ✅ | ❌ 无 `EphemeralRunner` 对应物 |

两边观测深度**天然不对称**：GitHub 半边能看到「扩容慢还是平台派发慢」，GitCode 半边只有 API 视角。大盘上必须显式标注，否则「没数据」会被误读成「没跑任务」。

### 1.4 阶段划分与数据源对应

一条 job 从被触发到跑完，按时间轴切成四段。分段是为了**分诊**：慢在 ① 去找平台侧，慢在 ② 是扩容或节点池资源问题，慢在 ③ 是调度与镜像问题，慢在 ④ 才是任务本身慢。

| 段 | 名称 | 从哪到哪 | 这一段在等什么 | GitHub 侧来源 | GitCode 侧来源 |
| --- | --- | --- | --- | --- | --- |
| ① | 平台排队 | job 创建 → 被某个 scale set 领取 | GitHub 队列积压、仓库并发上限 | 无直接指标；用 API `job.started_at − job.created_at` 减去 startup 直方图反推 | 无（只能拿到 ①②③ 合计） |
| ② | 扩容 | scale set 领取 → runner Pod 创建出来 | 扩缩容策略、节点池有没有可用卡 | 与 ③ 合并计时，拆不开 | 无（含在下面的合计里） |
| ③ | 启动 | Pod 创建 → runner 注册完成并拿到 job | 调度等待、镜像拉取、注册握手 | `gha_job_startup_duration_seconds`（= `RunnerAssignTime − ScaleSetAssignTime`，含 ②+③） | `apply_time → start_time`，是 ①②③ 的合计 |
| ④ | 执行 | job 开始执行 → job 结束 | 任务本身（编译、测试、构建镜像） | `gha_job_execution_duration_seconds`，或 API `job.completed_at − job.started_at` | API `end_time − start_time` |

两个约束要写清楚，否则容易在盘上做出量不出来的图：

- **② 和 ③ 在 ARC 的指标里拆不开**。listener 只暴露两条耗时直方图，startup 的起点是 scale set 领取 job 的时刻、终点是 runner 注册完成，中间囊括扩容、调度、拉镜像、注册四件事，没有指标能把它们分开。真要拆只能由三期 event agent 记下 Pod 的创建/Ready 时刻，本期不做。
- **GitCode 侧只有 ①②③ 合计和 ④**。它的 job 只有 `apply_time` / `start_time` / `end_time` 三个时间戳，排队与启动是一整块，分段能力天然比 GitHub 弱——这也是 1.3 说两边不对称的具体体现。

## 2. 指标清单

所有指标都带 `platform` 标签，取值 `github` / `gitcode`。

### 2.1 结果与耗时（aggregator）

| 指标 | 类型 | 标签 | 用途 | 期次 |
| --- | --- | --- | --- | --- |
| `ci_workflow_run_result_total` | counter | `platform` `repo` `workflow` `conclusion` `event` `branch_class` | 失败率主指标 | 一期 |
| `ci_workflow_job_result_total` | counter | `platform` `repo` `workflow` `job` `conclusion` `branch_class` | 定位哪个 job 挂了 | 一期 |
| `ci_workflow_run_duration_seconds` | histogram | `platform` `repo` `workflow` `conclusion` | run 时长分布 | 一期 |
| `ci_workflow_job_duration_seconds` | histogram | `platform` `repo` `workflow` `job` `conclusion` | job 执行时长 | 一期 |
| `ci_workflow_job_queue_duration_seconds` | histogram | `platform` `repo` `workflow` `job` | job 排队时长（job 轮询） | 一期 |
| `ci_workflow_consecutive_failures` | gauge | `platform` `repo` `workflow` `event` `branch_class` | 破损状态：连续失败次数，成功即归零 | 一期 |
| `ci_workflow_last_success_timestamp_seconds` | gauge | `platform` `repo` `workflow` `event` `branch_class` | 多久没成功过 | 一期 |

**`conclusion` 的取值策略：GitHub 侧原值透传，不做归并。**

- 平台已经把失败分好类，压平会把分诊信息丢光：`startup_failure` 是 workflow 本身没起来，`timed_out` 是撞了超时上限，`action_required` 是等人工批准——三者的处理人、处理动作与 `failure` 完全不同。
- GitCode 侧只有 `success` / `failure` / `cancelled` / `skipped` 四类，**两边取值集合天生不同**，跨平台查询必须同时带 `platform`。
- 与 semconv 的取值映射（`cancelled` → `cancellation`、`skipped` → `skip`、`timed_out` → `timeout`、`startup_failure` → `error`）只在 OTLP 导出层做，落盘与大盘一律保持平台原值——与 [spec/otlp-mapping.md](../spec/otlp-mapping.md) 处理日志字段是同一套做法。

**「失败」的口径**（失败率、连续失败、告警共用这一份定义，别各写各的）：

| 归类 | 取值 |
| --- | --- |
| 计入失败 | `failure`、`timed_out`、`startup_failure` |
| 不计入失败，但进分母 | `success`、`cancelled`、`skipped`、`neutral`、`stale` |
| 不计入失败，也不进分母 | `action_required`（等人工批准，从未执行） |

`action_required` 的 `created_at == updated_at`、零时长，**不记 `ci_workflow_run_duration_seconds`**（否则 0 秒样本会拉低 p50/p99）；但它照常计入 `run_result_total{conclusion="action_required"}`，用来看"有多少外部 PR 卡在等批准"。批准后平台会以 `run_attempt + 1` 重跑并给出真结论，那一条会被当作新的幂等键入账。

### 2.2 在途状态（aggregator，路径 B）

| 指标 | 类型 | 标签 | 用途 | 期次 |
| --- | --- | --- | --- | --- |
| `ci_workflow_runs_queued` | gauge | `platform` `repo` | 当前排队数 | 一期 |
| `ci_workflow_runs_in_progress` | gauge | `platform` `repo` | 当前在跑数 | 一期 |
| `ci_workflow_oldest_queued_age_seconds` | gauge | `platform` `repo` | 最久排队了多久 | 一期 |
| `ci_workflow_oldest_running_age_seconds` | gauge | `platform` `repo` | 最久执行了多久 | 一期 |
| `ci_workflow_oldest_job_queue_age_seconds` | gauge | `platform` `repo` | 最久卡住的单个 job | 一期 |

在途指标是**瞬时值**，告警直接用阈值比较，不走 `increase`，因此没有「窗口内攒样本」的延迟。

### 2.3 自监控（aggregator）

| 指标 | 类型 | 标签 | 用途 |
| --- | --- | --- | --- |
| `ci_workflow_last_sync_timestamp_seconds` | gauge | `platform` | 采集活性 |
| `ci_workflow_api_errors_total` | counter | `platform` `repo` `kind` | 401/403/429/超时分类计数 |
| `ci_workflow_rate_limit_remaining` | gauge | `platform` | 配额余量 |
| `ci_workflow_runs_finalized_total` | counter | `platform` `repo` `source` | 按来源（轮询/事件）计终态条数 |
| `ci_workflow_unresolved_runs` | gauge | `platform` `repo` | 进入在途但迟迟没有终态的 run 数 |
| `ci_workflow_list_truncated_total` | counter | `platform` `repo` `path` | 首页满页（可能截断）次数 |
| `ci_workflow_completed_page_overflow_total` | counter | `platform` `repo` | 一轮内新增数占满整页（可能漏采） |

### 2.4 集群侧容量与阶段（ghalistener，二期）

| 指标 | 类型 | 对应字段 | 用途 |
| --- | --- | --- | --- |
| `gha_job_startup_duration_seconds` | histogram | `RunnerAssignTime − ScaleSetAssignTime` | ②+③ |
| `gha_job_execution_duration_seconds` | histogram | `FinishTime − RunnerAssignTime` | ④ |
| `gha_started_jobs_total` / `gha_completed_jobs_total` | counter | — | 与轮询结果交叉校验 |
| `gha_assigned_jobs` / `gha_running_jobs` | gauge | — | 当前派单/在跑 |
| `gha_registered_runners` / `gha_busy_runners` / `gha_idle_runners` | gauge | — | 已注册/占用/空转 |
| `gha_min_runners` / `gha_max_runners` / `gha_desired_runners` | gauge | — | 扩缩容意图值 |

`gha_*` 没有 `platform` 标签——它只由 GitHub 侧产出，`platform` 恒为 `github`，不构成维度。

### 2.5 事件侧（ci-event-agent，三期）

三期**不新增业务指标**。事件路径不产出另一套指标，它做的是让 `ci_workflow_*` 更快、更全地拿到同一批事实：重跑补齐、精确完成时刻。入账仍在 aggregator，按幂等键合并，两条路径的产出用 `ci_workflow_runs_finalized_total{source}` 分开计数。agent 自身只需存活类自监控，不单列。

## 3. 分期规划

| 期次 | 组件 | 观测能力 | 工期 | 主要风险 |
| --- | --- | --- | --- | --- |
| **一期** | aggregator（GitHub + GitCode 两个 `Source` 适配器、终态轮询 / 在途轮询 / job 轮询、截断守卫、指标、告警、大盘） | run/job 结果与耗时、在途排队与执行、失败率与连续失败 |  | 单点；凭据失效会静默归零；GitCode 限流规则未公开，接入前要实测 |
| **二期** | 各集群 Prometheus agent 加 scrape job（A 步）；536 个 scale set 的 `listenerMetrics` 声明（B 步） | A 步先拿 8 个容量 gauge（池子够不够、扩缩容意图与实际差多少）；B 步再补 ②+③ 启动耗时与 ④ 执行耗时直方图、job 级排队 |  | A 步不重启任何组件；B 步改 values 会重建 listener pod，需分批 |
| **三期** | 各集群 `ci-event-agent` | run 身份、重跑补齐、精确完成事件 |  | 取决于 GitCode 侧 runner 形态 |

一期的两个适配器同步落地：GitHub 先行，GitCode 在 `Source` 接口定稿后并行接入。**契约与接口必须一次到位**——`platform` 标签、枚举归一、排序键抽象晚加等于全部序列换名。GitCode 接口实测若有阻塞，允许它的适配器在一期后段交付，但不允许先只做 GitHub 再回头改契约。

### 3.1 告警内容

一期产出两类面向人的告警：**结果层**（workflow 失败）与**在途层**（排队过久）。自监控类告警（凭据失效、采集停摆等，见 2.3）规则形态相同，不在此重复。两条规则都在中心 Prometheus 评估，经 Alertmanager 发邮件。

**失败告警按"一次失败意味着什么"分成五类，不做"每次失败都报"。** 先把实测量级摆出来，否则很容易设计成每天几百封：

| 口径（`vllm-ascend`，最近 2000 条 run / 21.9 小时） | 折算每天 |
| --- | --- |
| 全部事件的失败 | 251 |
| 原规则 `schedule` / `push` / `workflow_dispatch` 的失败 | 19.7 |
| 其中 `workflow_dispatch` 失败 | 17.5 |
| 其中 `schedule` 失败 | 2.2 |

原规则的 18 条里有 10 条是 bot（`vllm-ascend-ci`）用 `workflow_dispatch` 派发的 **PR 验证**（run 名形如 `Nightly-A3 (PR) 19513`），失败属于该 PR 作者，不属于值班人；另有 `ms-swift` 的 26 条 push 失败**全部**落在 feature 分支。这两类在 API 字段上都无法与真回归区分——PR 验证 run 的 `pull_requests` 为空、`head_branch` 同为 `main`、`run-name` 由仓库自定不可靠。**所以不靠字段猜，靠收窄事件来源 + 状态化告警。**

| 编号 | 告警 | 用来发现什么问题 | 依据（判据） | 覆盖 | vllm-project 实测 | triton-lang 实测 |
| --- | --- | --- | --- | --- | --- | --- |
| L1 基建 | `CIWorkflowInfraFailure` | **workflow 根本没跑起来**：YAML 无效、引用的 reusable workflow 不存在、撞了 `timeout-minutes`。不是代码问题，重试也没用，且 Actions 页面上没有日志 | `conclusion ∈ {startup_failure, timed_out}`，10 分钟内出现即报，`for: 1m` | 全部 event、全部分支 | 0 次 / 7 天 | 0 次 / 7 天 |
| L2 门禁 | `CIWorkflowGateBroken` | **主干可能已经坏了**：默认分支的构建/测试连续红，所有人的合入都被堵 | `consecutive_failures > 0`（首次失败即报），`for: 5m` | 默认分支的 `schedule` / `push` | 14 趟 → 约 2.0 封/天 | 7 趟 → 约 1.0 封/天 |
| L2′ nightly | `CIWorkflowNightlyBroken` | **各硬件 nightly 出现真回归**：连续两次红，不是单次 flaky | `consecutive_failures >= 2`，`for: 5m`，path 白名单 | `workflow_dispatch` | 29 趟 → 约 4.1 封/天 | 0（无 nightly） |
| L3 静默 | `CIWorkflowNoSuccess` | **定时任务静默死亡**：cron 被删、workflow 被停用、`if:` 恒假——不再产生 run，L2 永远看不见，直到某天要那份产物才发现 | `time() - last_success_timestamp > 86400` 且 `consecutive_failures == 0`，`for: 30m` | `schedule` | 3 条候选，排辅助 workflow 后 0 | 1 条，排后 0 |
| L4 PR 突增 | `CIWorkflowPRGateAnomaly` | **某个 workflow 的 PR 全面变红**：runner 镜像、依赖源、配置坏了——所有作者一起挂，不是某个人写错 | 1 小时 PR 失败率 `> 0.8` 且同期样本 `>= 10`，`for: 10m` | `pull_request` / `pull_request_target` | 约 1 封/天 | 0 |
| — | 不上告警 | **作者的正常迭代**（实测平均 2.4 次/PR，GitHub 已通知本人）、**合并队列重复验证**（`mergify[bot]`）、**并发取消**（`vllm` 72 小时 949 次）、**辅助 bot 自红** | 不进任何规则 | PR 逐次失败、dispatch 逐次失败、feature 分支、`cancelled` | 0 封 | 0 封 |

统计口径：`vllm-project` / `triton-lang` 两个组织共 58 个非归档仓，窗口 2026-10-19 → 10-26 共 7 天，用 `created` 过滤 + `total_count` 取精确失败数（run 总数会在 2500 处被 GitHub 截断，失败数不截断）。两组织失败总量 **820 + 72 = 892 次/天**，上面的五类合计约 **8 封/天**——差 100 倍，差别不在"报不报"，而在"同一个问题报几次"。

**为什么不统一成"失败即报"**：那是一条 `increase(ci_workflow_run_result_total{conclusion="failure"}[1m]) > 0`，能写，但输出 892 封/天，且其中绝大部分对收件人没有信息增量——

- **同一问题重复收费**：`cann-container-image` 一趟破损连红 15 次、`AReaL Nightly CI` 连红 24 次，逐次报就是 15 封、24 封同一个问题。
- **不同主体混检**：PR-A 失败、PR-B 成功、PR-A 再失败，合并成一条序列后"连续失败"变成在数无关 PR 的交替。实测把 PR 混成一条序列会有 5%～12% 的 run 在翻转状态，反而产出 51～89 趟/天/仓。
- **很多"失败"不是失败**：`cancelled` 实测 `vllm` 72 小时 949 次（并发组掐旧 run）、`Ready Label` 占了 `vllm-torchtpu` 186 次 PR 失败的 132 次、`mergify[bot]` 在合并队列里重复验证 242 次/3 天。

**告警按"信息增量"触发，不按"事件发生"触发**。下面五类的差别不是报不报，而是"这个 event 的一次失败意味着什么"。

**L1 基建类**（结论本身就是原因，不需要看日志）：

```yaml
- alert: CIWorkflowInfraFailure
  expr: |
    increase(ci_workflow_run_result_total{
      conclusion=~"startup_failure|timed_out"
    }[10m]) > 0
  for: 1m
  labels:
    severity: critical
    cluster: central
  annotations:
    summary: "{{ $labels.repo }} / {{ $labels.workflow }} 未能正常执行（{{ $labels.conclusion }}）"
    description: >-
      仓库 {{ $labels.repo }}（{{ $labels.platform }}）的 workflow「{{ $labels.workflow }}」
      在最近 10 分钟内出现 {{ $labels.conclusion }}，触发方式 {{ $labels.event }}。
      这不是业务代码失败：startup_failure 说明 workflow 本身没起来，timed_out 说明撞了 timeout-minutes。
    runbook: >-
      1) startup_failure：查 workflow 文件与它引用的 reusable workflow 是否有效——这类 run 一个 job 都没有，Actions 页面不会有日志；
      2) timed_out：查 job 的 timeout-minutes 与是否单点卡死，必要时放宽上限并拆 job；
      3) 两类都不属于上游代码问题，处理完在本仓记录一次。
```

**L1 是唯一不按事件/分支收窄的一层**：`startup_failure` 意味着一个 job 都没起来，`timed_out` 意味着撞了 `timeout-minutes`——两者都与"谁触发的"无关，PR 触发时同样是我们的问题。刷屏风险交给 Alertmanager 分组收敛（同一 `repo`/`workflow` 30 分钟一封），不靠规则过滤掉。

**L2 门禁类**（状态型：破损期间只占一个告警实例，成功即恢复）：

```yaml
- alert: CIWorkflowGateBroken
  expr: |
    ci_workflow_consecutive_failures{
      event=~"schedule|push",
      branch_class="default"
    } > 0
  for: 5m
  labels:
    severity: critical
    cluster: central
  annotations:
    summary: "{{ $labels.repo }} / {{ $labels.workflow }} 门禁连续失败"
    description: >-
      仓库 {{ $labels.repo }}（{{ $labels.platform }}）的 workflow「{{ $labels.workflow }}」
      已连续失败 {{ $value }} 次，触发方式 {{ $labels.event }}。
      本次破损始于最近一次成功之后，任一次成功即自动恢复。
    runbook: >-
      1) 打开 https://github.com/{{ $labels.repo }}/actions 按 workflow 名筛选最近一次红叉；
      2) 若是主干门禁，先确认 main 是否可合入，必要时回滚最近一次合并；
      3) flaky 用例请在 workflow 里标记或隔离，不要长期靠重跑。
```

**L2 已经是"出现就报"，不是"连续两次才报"**：`consecutive_failures` 在第一次失败后就是 1，`for: 5m` 一到立刻发。状态型只影响后续——同一趟破损里再红 14 次不再发，直到一次成功转 `[RESOLVED]`。默认分支失败一次就意味着 main 可能已经坏了、所有人被堵，这个代价不值得等第二次确认。

`ci_workflow_consecutive_failures` 的计数规则：只累加 `failure` / `timed_out` / `startup_failure`；`success` 归零；`cancelled` / `skipped` / `neutral` / `stale` / `action_required` **既不累加也不归零**——它们不代表"这次门禁通过了"，否则一次 `skipped` 就会把破损状态抹掉。

实测破损（已排除辅助 workflow，两组织 7 天共 21 趟）：`vllm-gaudi hourly-ci` 3、`vllm pre-commit` 2、`semantic-router main` 2、`triton ci.yml` 2、`triton-ascend ci.yml` 2，其余 `vllm-metal parity`、`vllm-xpu-kernels ut`、`tpu-inference pre-commit`、`vllm-sail upstream-reuse / upstream-canary`、`vllm-ascend e2e_upstream_test / doc_linkcheck`、`triton / triton-ascend llvm-build`、`triton-ascend npuir-build` 各 1。

**L2′ nightly**（`workflow_dispatch` 派发的定时回归，需要连续两次）：

```yaml
- alert: CIWorkflowNightlyBroken
  expr: |
    ci_workflow_consecutive_failures{
      event="workflow_dispatch",
      branch_class="default",
      workflow=~"schedule_nightly_test_a3(_560t)?|schedule_nightly_test_a5|schedule_nightly_test_a2|schedule_nightly_test_310p|schedule_daily_test|schedule_test_coverage"
    } >= 2
  for: 5m
  labels:
    severity: critical
    cluster: central
  annotations:
    summary: "{{ $labels.repo }} / {{ $labels.workflow }} 回归连续失败"
    description: >-
      仓库 {{ $labels.repo }}（{{ $labels.platform }}）的回归任务「{{ $labels.workflow }}」
      已连续失败 {{ $value }} 次。
    runbook: >-
      1) 打开该仓 Actions 页，按 workflow 名筛选最近两次红叉，对比第一次失败的用例；
      2) 连续两次同一用例失败才是回归，不同用例失败优先怀疑 runner 或环境；
      3) 确认是回归后按硬件类型（A2 / A3 / A5 / 310P / 560T）通知对应维护人。
```

**为什么这条要 `>= 2` 而 L2 是 `> 0`**：`vllm-ascend` 的 nightly 全由 bot 用 `workflow_dispatch` 派发（文件名叫 `schedule_*`，但 7 天内 0 次 `schedule` 事件），单次红大多是 flaky。实测首次即报 **9 封/天**、连续 2 次 **4.1 封/天**、连续 3 次 2.3 封/天。选 2 次：既滤掉单次抖动，又不把真回归拖到第三天。

实测（7 天，阈值 ≥2）：`schedule_nightly_test_a5` 8 趟、`a3` 6 趟、`a3_560t` 6 趟、`schedule_daily_test` 3 趟、`a2` / `310p` / `weekly_test_a3` / `test_coverage` 各 1～2 趟。triton 侧 7 天只有 6 次 dispatch、1 次失败，不需要白名单。

**白名单要按 `workflow` 标签（`run.path` 的文件名）匹配，不能按 `run.name`**：这类 workflow 的 run name 带 PR 号（`Nightly-A3 (PR) 19513`），按名字匹配会让每个 PR 都成为一条新序列，实测把 vllm-project 的破损趟数从 30 抬到 113。

**替代做法**：让上游给这 5 个 yaml 补上真正的 `schedule:` 触发，它们会自动落进 L2/L3，白名单可以整段删掉。这要改 vllm-ascend 的仓，需单独提 issue。

**L3 兜底**（防定时任务长期静默——L2 只能发现"连续失败"，发现不了"从某天起再没跑过"）：

```yaml
- alert: CIWorkflowNoSuccess
  expr: |
    time() - ci_workflow_last_success_timestamp_seconds{
      event="schedule",
      branch_class="default"
    } > 86400
    and on(platform, repo, workflow, event)
    ci_workflow_consecutive_failures == 0
  for: 30m
  labels:
    severity: warning           # 需先在黑洞之前补 CI 路由，否则被丢弃，见「模板约束」
    cluster: central
  annotations:
    summary: "{{ $labels.repo }} / {{ $labels.workflow }} 已超过 24 小时没有成功"
    runbook: >-
      1) 先看 ci_workflow_consecutive_failures 是否在涨；
      2) 低频 workflow（周任务）把阈值放宽到 7d，或从本规则中排除；
      3) 若该 workflow 已被停用，连同规则一起下线，不要长期挂着 firing。
```

**L3 只认 `schedule`，不认 `workflow_dispatch`**：手工/机器人派发的 workflow 天然是低频的——实测 `cann-container-image` 有 5 个发布类 workflow 常年"24 小时没成功"，它们只是没人派发，不是坏了。把 dispatch 纳入 L3 会得到一屏恒 firing 的噪声，且无法用字段区分"每月发布一次"与"回归断了"。dispatch 派发的每日回归由 L2′ 用白名单承接，不进 L3。

**L3 必须排掉已经在 L2 firing 的序列**（上面那个 `and ... consecutive_failures == 0`）：实测 5 条候选里有 3 条同时也在 L2 报（如 `AReaL` 的 `Nightly CI` 24 天没成功一次），不排就是同一根因两封邮件、两个告警名，而且 L3 是 warning、L2 是 critical，收件人会先看到轻的那封。

**`ci_workflow_last_success_timestamp_seconds` 在"从未成功过"时必须落 `0`，不能缺失。** 缺失的 series 在 PromQL 里等于不存在，L3 对刚接入、一次都没跑通过的 workflow 完全失明——而"新接入的定时任务压根跑不起来"恰恰是最该报的一种。落 0 后既命中 `time() - 0 > 86400`，邮件正文也能一眼看出是从未成功。

实测候选：`vllm-gaudi scorecard` 162h、`guidellm container-maintenance` 116h、`vllm-ascend doc_translate` 27h、`llvm-project release-binaries` 51h——**全是辅助 workflow**，按排除清单过滤后为 0。`doc_translate` 那 27 小时属于周任务误报，runbook 里靠"放宽到 7d 或排除"处理。

**L4 PR 门禁突增**（按 workflow 聚合，不按 PR）：

```yaml
- alert: CIWorkflowPRGateAnomaly
  expr: |
    (
      sum by (platform, repo, workflow) (
        rate(ci_workflow_run_result_total{event=~"pull_request.*", conclusion=~"failure|timed_out"}[1h])
      )
      /
      sum by (platform, repo, workflow) (
        rate(ci_workflow_run_result_total{event=~"pull_request.*"}[1h])
      )
    ) > 0.8
    and
    (
      sum by (platform, repo, workflow) (
        increase(ci_workflow_run_result_total{event=~"pull_request.*"}[1h])
      ) >= 10
    )
  for: 10m
  labels:
    severity: critical
    cluster: central
  annotations:
    summary: "{{ $labels.repo }} / {{ $labels.workflow }} 的 PR 检查大面积失败"
    description: >-
      仓库 {{ $labels.repo }}（{{ $labels.platform }}）的 workflow「{{ $labels.workflow }}」
      最近 1 小时的 PR 失败率已超过 80%。这是所有 PR 一起挂，通常不是某个作者写错，
      而是 runner 镜像、依赖源或 workflow 配置出了问题。
    runbook: >-
      1) 随便挑两个失败的 PR run，看是不是同一个 job 在 setup 阶段就挂；
      2) 查最近的 workflow 文件变更、runner 镜像 tag、依赖源可达性；
      3) 不要逐个通知 PR 作者——先修 workflow，修完让他们重跑。
```

**为什么 PR 侧不逐条报**：PR 失败平均 **2.4 次/PR**（`vllm-ascend` 3 天里 249 个 PR 贡献 607 次失败，`triton-ascend` 有单个 PR 红 15 次），归属人是作者本人，而 GitHub 原生通知已经按人送达。更要紧的是**指标结构做不出"每个 PR 一封"**：要按 PR 告警就得有 `pr` 或 `head_sha` 标签，这正是契约禁止的高基数维度；合并成 `(repo, workflow)` 一条序列后，实测 5%～12% 的 run 会让状态翻转，反而更糟。所以 PR 侧只报"整体崩塌"这一种。

**0.8 与 ≥10 两个阈值都是实测定的**：`vllm pre-commit` 基线 25.7%、`vllm-ascend pr_test` 48.4%、`triton-ascend pre-commit` 28.6%、`ci.yml` 18.5%，离 0.8 都有余量；`≥10` 的样本下限不能省——`triton-ascend docs-rtd-preview` 是 8/8 = 100%，没有下限就会每条 PR 都报。1 天试算：3 个主力仓命中 1 次。

如果某个 workflow 的常态失败率本身就在 80% 以上，用固定阈值会恒 firing，需把它加进排除清单——真实门禁不该长期红的。

**必须排除的 workflow**（不排就会让上面的量级翻倍）：

| 类别 | 实例 | 实测噪声 |
| --- | --- | --- |
| 标签 / 评论 / 机器人 | `community-labels`、`stale_manage`、`Ready Label`、`pr-title-check` | `community-labels` 一个占 vllm-project L2 原始 30 趟里的 10 趟；`Ready Label` 占 `vllm-torchtpu` 186 次 PR 失败里的 132 次 |
| 安全 / 评分 | `scorecard`、`trivy` | L3 候选里占 1 条 |
| 发布 / 构建 | `wheels`、`docker-push-images`、`publish-fa4`、`release-binaries-all` | triton-lang L2 原始 14 趟里占 5 趟 |
| 平台自动添加 | `Scorecard supply-chain security`（GitHub 自动创建） | `verl` 2 趟破损里 1 趟 |

配置形态：`repo → workflow 排除 pattern` 的一张表，随 aggregator 的仓库配置下发。**按 `workflow` 标签匹配，而 `workflow` 标签取 `run.path` 的文件名去掉扩展名**（`schedule_nightly_test_a3`），**不取 `run.name`**——后者在用了动态 `run-name:` 的仓库里每次 run 都不同（`Nightly-A3 (PR) 19513`），用它做身份等于每个 PR 都是一条新序列。`run.name` 只写进邮件正文。

用户收到的邮件（L2，时延约 1～2 分钟）：

> **主题**：`[CRITICAL][central] CIWorkflowGateBroken`
>
> **🔴 FIRING — CIWorkflowGateBroken @ central**
>
> | 字段 | 内容 |
> | --- | --- |
> | 集群 | central |
> | 告警 | CIWorkflowGateBroken |
> | 状态 | firing |
> | 开始时间 | 2026-10-26 09:12:33 CST |
> | 描述 | 仓库 vllm-project/vllm-ascend（github）的 workflow「schedule_e2e_upstream_test」已连续失败 1 次，触发方式 schedule。本次破损始于最近一次成功之后，任一次成功即自动恢复。 |
>
> **修复建议**
>
> 1) 打开 https://github.com/vllm-project/vllm-ascend/actions 按 workflow 名筛选最近一次红叉；2) 若是主干门禁，先确认 main 是否可合入，必要时回滚最近一次合并；3) flaky 用例请在 workflow 里标记或隔离，不要长期靠重跑。
>
> `Alertmanager | 监控看板 | 本邮件由 CI 基础设施监控系统自动发送，请勿回复。`

**连续失败不逐封发送**：破损期间该规则一直处于 firing，同一 `(repo, workflow)` 只占一个告警实例，`group_interval` 30 分钟最多刷新一封。第一次成功即转 `[RESOLVED]`，主题变 `[RESOLVED][central] CIWorkflowGateBroken`，正文标题变 `✅ RESOLVED`，并多一行「恢复时间」。

**排队过久告警**：告警对象是仓库，不是单个 run——指标按仓聚合，取的是该仓最久的排队时长。

```yaml
- alert: CIWorkflowQueueBacklog
  expr: ci_workflow_oldest_queued_age_seconds > 1800
  for: 10m
  labels:
    severity: critical        # 定 warning 会被中心黑洞吞掉，见下文「模板约束」
    cluster: central
  annotations:
    summary: "{{ $labels.repo }} 有 run 排队超过 30 分钟"
    description: >-
      仓库 {{ $labels.repo }}（{{ $labels.platform }}）队列积压：最久的 run 已等待
      {{ $value | humanizeDuration }}。常见原因是仓库并发上限已满或 runner 池没有空闲机器。
    runbook: >-
      1) 看同仓 runs_queued 是否持续上涨；
      2) 查 ARC 对应 scale set 的 desired/running runner 是否卡在 0；
      3) 若是长期排队而非故障，考虑调高该仓 concurrency 或扩卡池。
```

用户收到的邮件（时延约 7 分钟：B 路径 5 分钟轮询 + 抓取 + 评估）：

> **主题**：`[CRITICAL][central] CIWorkflowQueueBacklog`
>
> **🔴 FIRING — CIWorkflowQueueBacklog @ central**
>
> | 字段 | 内容 |
> | --- | --- |
> | 集群 | central |
> | 告警 | CIWorkflowQueueBacklog |
> | 状态 | firing |
> | 开始时间 | 2026-10-26 10:28:10 CST |
> | 描述 | 仓库 vllm-project/vllm-ascend（github）队列积压：最久的 run 已等待 47m0s。常见原因是仓库并发上限已满或 runner 池没有空闲机器。 |
>
> **修复建议**
>
> 1) 看同仓 runs_queued 是否持续上涨；2) 查 ARC 对应 scale set 的 desired/running runner 是否卡在 0；3) 若是长期排队而非故障，考虑调高该仓 concurrency 或扩卡池。

**模板约束**（中心 Alertmanager 现有 `email.infra.html`，接入前必须处理）：

- **`cluster` 标签不能缺**。主题模板是 `[{{ severity }}][{{ cluster }}] {{ alertname }}`，正文标题是 `@ {{ cluster 或 project }}`；两者都没有时会渲染成 `[CRITICAL][] …` 和 `@ `。规则里显式写 `labels: cluster: central`，或给 CI 告警单独一个 receiver 改用 `{{ or .CommonLabels.cluster .CommonLabels.repo }}`（`datastat-email` 已是这种写法）。
- **`severity: warning` 默认被黑洞丢弃**（路由树在 warning 之前没有 CI 前缀规则）。新告警要么定为 critical，要么在黑洞之前补 `alertname =~ "CIWorkflow.*"` 的路由。
- **`repo` / `workflow` / `platform` 不会出现在表格里**：模板只渲染 `cluster` / `project` / `pod` / `node` / `instance` / `deployment` / `mount` / `endpoint`。要么把定位信息写进 `description`（上面两条就是这么做的），要么给模板加「仓库 / Workflow / 平台」三行。
- **`runbook` 必填**：模板的「修复建议」表直接引用 `.Annotations.runbook`，缺了就是一格空白。
- **`group_by` 要加维度**：根路由是 `[cluster, alertname]`，同一仓多个 workflow 失败会被并进同一封（`group_interval` 30 分钟）；需要按 workflow 分开时，在 CI 路由里改为 `[platform, repo, workflow, alertname]`。

## 4. 详细设计

### 4.1 中心 aggregator

**内部结构**

```
┌──────────────── ci-workflow-aggregator ────────────────┐
│  scheduler ──┬─ A  completed 首页        每 60s         │
│              ├─ B  queued + in_progress  每 5min        │
│              ├─ B+ 活跃 run 的 /jobs     每 5min        │
│              └─ 状态落盘 / 剪枝          每 30s 或 N 条  │
│                                                         │
│  Source 接口 ── githubAdapter / gitcodeAdapter           │
│  状态层    ── seen（去重）/ inflight（在途）/ 每仓统计   │
│  出口      ── /metrics（Prometheus 抓取）                │
│            ── /ingest（三期接收 agent 事件，带 token）   │
└─────────────────────────────────────────────────────────┘
```

**归一数据模型**

```go
type Run struct {
    Platform   string    // github | gitcode
    Repo       string    // owner/name
    ID         string    // GitHub 数字 id / GitCode 32 位 hex
    Attempt    int
    Workflow   string
    Status     string    // 归一后：queued|running|completed
    Conclusion string    // 归一后：success|failure|cancelled|skipped|...
    Event      string
    OrderKey   time.Time // GitHub created_at / GitCode start_time
    StartedAt  *time.Time
    FinishedAt *time.Time
}
```

`Source` 只做三件事：`ListRuns(status, since, page)`、`GetRun(id)`、`ListJobs(id)`。去重、状态机、指标、告警全部平台无关。

**状态归一（适配器最核心的一条）**：GitHub 的 run 有 `status`（阶段：`queued` / `in_progress` / `completed`）和 `conclusion`（结论）**两个**字段；GitCode 只有**一个** `status`，阶段和结论混在同一个值里。适配器必须拆开：`RUNNING` → `Status=running`；`COMPLETED` / `FAILED` / `CANCELED` / `IGNORED` → `Status=completed`，结论按下表映射。`PAUSED` / `SUPPEND` 的结论语义未验证，先各自保留成独立取值，不并入成功或失败。拆完之后，上层的状态机只认 `Status` / `Conclusion` 两个字段，不再关心平台。

**GitHub 侧 `conclusion` 原样透传，不做语义归并**（理由见 2.1 的取值策略）。GitCode 侧没有这些细类，它的 `status` 归一到共享词汇中它能表达的那部分：

| 含义 | 归一值 | GitHub（原值透传） | GitCode（归一后） |
| --- | --- | --- | --- |
| 成功 | `success` | `success` | `COMPLETED` |
| 失败 | `failure` | `failure` | `FAILED`（可能带后缀，取 `split("(")[0]`） |
| 取消 | `cancelled` | `cancelled` | `CANCELED` |
| 跳过 | `skipped` | `skipped` | `IGNORED` |
| 超时 | `timed_out` | `timed_out` | 无（平台只给 `FAILED`） |
| 等人工 | `action_required` | `action_required` | 无 |
| 未定 / 其他 | 原样保留 | `neutral` / `stale` / `startup_failure` / 将来新增值 | `PAUSED` / `SUPPEND`，不并入成功/失败 |

**枚举是开放的，不要假设闭合**：`startup_failure` 并不在 GitHub 官方 REST 文档（workflow-runs）的枚举里，但确实会返回——本批 6 个仓近 600 条 run 抽样中未出现，属低频而非不存在。适配器遇到未知取值一律原样透传、按"未定"处理，**不得**兜底成 `failure` 或 `success`。

`event` 同理归一：`MR` → `pull_request`、`Push` → `push`、`Manual` → `workflow_dispatch`；`Note` 语义未验证，先原样保留。

**标签取值**（指标 label 直接取自归一后的字段）：

- `repo` 用 `owner/name`；`workflow` 取 **`run.path` 的文件名去掉扩展名**（`.github/workflows/schedule_nightly_test_a3.yaml` → `schedule_nightly_test_a3`），**不用 `run.name`**。理由是实测的：`vllm-ascend`、`vllm-gaudi`、`vllm-sail` 都用了动态 `run-name:`（`Nightly-A3 (PR) 19513`、`PR #1883 [sha]`），按名字做身份会让每个 PR / 每个提交都成为一条新序列——实测把 vllm-project 的破损趟数从 30 抬到 113。文件没写 `name:` 时 `run.name` 还会回落到文件路径，同样不稳定。代价是改文件名会换 series，接受：改名本身就是换了身份，必要时在 aggregator 侧配别名映射。`run.name` 只用于邮件正文展示。
- `job` 取 job 名；矩阵 job 默认保留全名（`build (aarch64, py3.10)`），单维度取值超过 10 个时才截断到第一个 `(`。
- `branch_class` 取 `default` / `other`（`head_branch` 是否等于仓库默认分支；默认分支名从 `GET /repos/{r}` 取一次后缓存）。只有两个取值，用来把 feature 分支的失败挡在门禁告警之外——实测 `ms-swift` 的 26 条 push 失败**全部**落在 feature 分支，不做这层过滤就是纯噪声。
- **禁止**把 `run_id` / `job_id` / `head_sha` / `job_workflow_ref` 做成标签，高基数会撑爆时序。`status` 不进标签，只驱动状态机。

**三条轮询路径**

| 路径 | 频率 | 请求 | 职责 |
| --- | --- | --- | --- |
| A | 60s | `status=completed&per_page=100` | 短 run 的终态；本轮新 run 逐个加计数 |
| B | 5min | `status=queued` + `status=in_progress` 各一页 | 在途 gauge；run 消失后按 ID 核实终态 |
| B+ | 5min | 对在途 run 取 `/jobs` | job 级排队与执行时长，定位具体 job |

B+ 的调用量随活跃 run 数走（20~40 个 × 12 次/小时 ≈ 240~480 次/小时）。

**去重**

- 键：`<platform>:<repo>:<run_id>:<run_attempt>`。re-run 会让 `run_attempt` 递增，只按 `run_id` 会丢重跑。
- **不能用单条水位线 `max(run_id)` 推进**：完成顺序和创建顺序不一致，长任务会被水位线越过而永久跳过。
- 每次成功计数后刷新 `processed_at`。

**剪枝（两条同时满足才删）**

1. `order_key < 本轮 A 首页最老条目` —— 已经掉出首页，不会再被 A 看到；
2. `now − processed_at > 对账窗口`（默认 2 小时）—— 深扫不会再覆盖到它。

只按 TTL 剪会出问题：低频仓库首页可能覆盖几十天，按固定 TTL 剪掉的键会重新出现并被二次计数。

**终态判定**

在途 run 从 B 的列表里消失时，**不连续等两轮**，而是：

- 本轮两条列表请求都成功（HTTP 200 且解析成功）；
- 按 ID 取一次 `GetRun`；
- **以返回的 `status` 为准**：`completed` 才入账，仍是 `running`/`queued` 就留在 registry。
- 入账时再取一次 `ListJobs`（`/jobs`）刷新 job 级终态。B+ 每 5 分钟才采样一次，最后一次采样之后才结束的 job 会停在 `in_progress`，不补这一次就永远落不进 `ci_workflow_job_result_total`——而**撞 `timeout-minutes` 的 job 恰恰是最后才结束的那一批**，不补抓等于把 `timed_out` 系统性丢掉。

连续两轮的代价是长任务完成时又多等一个周期，而它本来就是为了防整页请求失败——用「请求成功校验」能直接覆盖，不需要多等。

**失败与降级**

- 每个仓库独立超时，**单仓失败不拖垮整轮**，失败时保留上一轮的值，只加 `api_errors_total`。
- 401/403 单独告警：它会让采集静默归零。
- 进程重启后把当前首页整体当基线写入、**不回填**——宁可缺最前面几十秒，也不要重复计数。

**守卫（替代原全局对账）**

| 守卫 | 判据 | 覆盖的风险 |
| --- | --- | --- |
| 列表截断 | B 的响应 `len(items) == per_page` | 排 100+ 时最久的 run 从没被看见 |
| 首页溢出 | A 本轮新 run 数 == `per_page` | 一分钟内完成超过 100 个，老的没看就被挤下去 |
| 未收敛 | registry 条目超过 48h 仍无终态 | B/B+ 失效 |
| 来源计数 | `runs_finalized_total{source}` 长期为 0 | 某条路径静默停止工作 |

注意判据用 `per_page` 而不是 `total_count`：`total_count` 会封顶在 2500，小规模下也与列表不符（实测 `queued` 为 32 vs 27），只能当上下文写进日志，不能参与判定。

**速率预算（GitHub，认证后 5,000 次/小时）**

| 路径 | 计算 | 次/小时 |
| --- | --- | --- |
| A | 21 仓 × 60 | 1,260 |
| B | 21 仓 × 2 条列表 × 12 | 504 |
| B+ | 20~40 个活跃 run × 12 | 240~480 |
| 合计 | | **约 2,000~2,250（45%）** |

表中「21 仓」是**开通了 ARC runner 的仓库数**，不是集群数也不是 scale set 数：它取自
`opensourceways/ascend-ci-deployment` 仓 `projects/<org>/<repo>/` 下存在 `values.yaml` 的目录，
按 `<org>/<repo>` 去重（一个仓可以有多个 scale set，只算一次），快照于该仓 commit `26ca84d1`（2026-10-20）。
集群文档站的 [Cluster Map](https://ascend-gha-runners.github.io/docs/Cluster/) 由同一份部署配置自动生成，
页面上标注的「21 Projects」与此一致，可用来交叉核对；部署新增仓库时按实际仓数线性重算这张表。

其余系数：A 的 60 = 每分钟一轮 × 60 分钟；B 的 12 = 每 5 分钟一轮 × 12，两条列表指 `queued` 与 `in_progress`；
B+ 的 12 同理，基数是该时刻在途的 run 数（实测 20~40）。

GitCode 的限流规则未公开，接入前要实测；适配器里要有独立的配额余量指标。

**两个适配器的差异**

| 项 | GitHub | GitCode |
| --- | --- | --- |
| 状态字段 | `status`（阶段）+ `conclusion`（结论）两个字段 | 只有一个 `status`，阶段与结论混在一起，需拆成两个归一字段（见上文「状态归一」） |
| 状态过滤 | `completed` 是**分组**，一个参数覆盖多种结论 | 每个取值单独，需多次调用或在本地过滤 |
| 排序键 | `created_at` | 无该字段，用 `start_time`（列表实测按 `run_number` 倒序） |
| 时间单位 | ISO8601 | 毫秒时间戳 |
| job 结构 | 平铺 `/jobs` | 嵌套 `stages[].jobs[]` |
| job 排队起点 | `created_at` | `apply_time`（部分为 null，需兜底） |
| 鉴权 | `Authorization: Bearer` | 同样用 Bearer；**不要用 `access_token` 查询参数**，会进访问日志 |

**部署形态**

Deployment 单副本，`/metrics:8080`；状态落 `/data/state.json`，PVC 1Gi `ReadWriteOnce` + `strategy: Recreate`（单副本前提下滚动升级不会出现两个 Pod 同时写）。不用 CronJob + Pushgateway：短生命周期进程的计数器每轮从 0 开始，`increase()` 只会看到重置。

状态文件写入用 tmp + fsync + rename 原子替换，每 30 秒或累积 200 条事件刷一次。

### 4.2 ghalistener

**它是什么**

ARC 每个 scale set 自带一个 listener，直连 GitHub Actions 的消息通道做派单，同时暴露 Prometheus 指标。**它是 ARC 的组成部分，不能停、不能改**，metrics 端点由 controller 以 `--listener-metrics-addr` 下发（参数在 Secret 里的 config.json）。

**现状（2026-10-23 实测）**

| 项 | 状态 |
| --- | --- |
| 二进制 | 全集群在跑，`arc-systems` 下 7 个集群合计 387 个 pod |
| metrics 端口 | pod 声明 `metrics:8080`，controller values 的 `metrics` 段已打开 |
| 指标声明 | 536 个 scale set 中 **500 个**声明了 `listenerMetrics`，36 个为空（`installMetrics` 只注册声明过的名字，不报错） |
| 中心采集 | **完全没有**，中心 Prometheus 里 `gha_*` 序列数为 0 |

**二期 A：只加抓取（零改动）**

在各集群 `prometheus-agent-configmap-patch.yaml` 增加一个 job：

```yaml
      - job_name: arc-listener
        kubernetes_sd_configs:
          - role: pod
            api_server: https://kubernetes.default.svc
            authorization:
              type: Bearer
              credentials_file: /etc/prometheus/sa-token/token
            tls_config:
              insecure_skip_verify: true
        relabel_configs:
          - source_labels: [__meta_kubernetes_namespace]
            regex: arc-systems
            action: keep
          - source_labels: [__meta_kubernetes_pod_label_app_kubernetes_io_component]
            regex: runner-scale-set-listener
            action: keep
          - source_labels: [__meta_kubernetes_pod_label_actions_github_com_repository]
            target_label: repository
          - source_labels: [__meta_kubernetes_pod_label_actions_github_com_scale_set_name]
            target_label: scale_set
          - target_label: cluster
            replacement: <集群名>
        metric_relabel_configs:
          - source_labels: [__name__]
            regex: "gha_(assigned|running)_jobs|gha_(registered|busy|min|max|desired|idle)_runners"
            action: keep
```

三件事不用改：

- **RBAC**：`base/monitoring-clusterrole.yaml` 已有全局 `pods: get/list/watch`；
- **remote_write**：现有丢弃规则按 namespace 匹配 `ascend-gha-runners`，listener 在 `arc-systems`，不受影响；
- **补充声明**：二期 A 只抓 gauge，36 个未声明的 scale set 里 gauge 也是空的，可先接受。

产出约 8 × 387 ≈ 3,000 条序列。

**二期 B：打开时长指标（需要改 values）**

必须**从 `listenerMetrics` 声明里去掉 `job_workflow_ref`**，不能在抓取侧 `labeldrop`：

- 该标签值形如 `owner/repo/.github/workflows/x.yml@refs/pull/123/merge`，ref 段无界；
- 抓取侧删标签会让同一 job 的 main 分支和 PR 分支变成两条同标签序列，Prometheus 报重复样本并丢弃其中一条，**counter 静默少算**；
- 从声明里去掉，listener 自己在源头就不按 ref 拆维度，counter 正确相加。

顺带把 46 个 bucket 精简到约 10 个。基数从约 1,960 条/listener 降到约 500 条/listener。

**风险：改 values 会重建 listener pod**

`listenerMetrics` 写进 listener 的 config Secret，改它等于换配置。listener 重建期间该 scale set 接不到新 job，job 会一直排队。500 个一起滚就是全网 500 个 scale set 同时短暂失能。

必须按集群/按仓库分批，避开 CI 高峰，每批之后观察 `gha_assigned_jobs` 与排队指标。

### 4.3 集群 event agent（三期）

**定位**

补 ghalistener 和轮询都拿不到的东西：**run 身份**和**重跑**。它不替代任何人。

| 它补什么 | 为什么别的源不行 |
| --- | --- |
| run/job 身份 | `gha_*` 的标签里没有 `run_id` / `job_id` |
| 重跑 | API 列表按创建时间排序，重跑旧 run 不会回到首页 |
| 精确完成时刻 | 轮询有 60s~5min 延迟 |

**watch 对象**

`actions.github.com/v1alpha1` 的 `EphemeralRunner`。它的 status 含 `WorkflowRunId`、`JobID`、`JobRepositoryName`、`JobWorkflowRef`、`JobDisplayName`、`Phase`、`Ready`、`Failures`；**没有时间戳**，所以创建/就绪时刻取 watch 事件的观测时间。

**事件模型**

```json
{
  "platform": "github",
  "cluster": "guiyang-004",
  "repo": "vllm-project/vllm-ascend",
  "run_id": "123456789",
  "job_id": "987654321",
  "job_name": "build-image",
  "phase": "completed",
  "observed_at": "2026-10-23T08:00:00Z"
}
```

事件里**只带 job 级事实**（`cluster`、`repo`、`run_id`、`job_id`、`phase`），**不带 `conclusion`、不带 `run_attempt`**：

- agent 判断不了 run 级结论——一条 run 的 job 可能跨多个集群，它只知道自己这几个 job 结束了；
- agent 也不知道 attempt。

两者都由 aggregator 收到事件后按 `run_id` 取一次 `GetRun` 拿权威值，再决定是否入账。

**协议与幂等**

- agent → aggregator：`POST /ingest`，批量提交，带共享 token（Secret 挂载）。
- aggregator 幂等键：`<platform>:<repo>:<run_id>:<attempt>:<job_id>:<phase>`。
- 同一 `(run_id, attempt)` 的 run 级指标只记一次，无论来自轮询还是事件。

**断线与回补**

- 用 list-watch + `resourceVersion` 续接；连接断开后重新 list 对齐，避免漏事件。
- 本地磁盘缓冲最近 N 条未确认事件，重试指数退避。
- **事件不是唯一依赖**：agent 挂掉时轮询仍在跑，只是退化为一期的能力；所以 agent 失效不该产生「数据全空」，只影响重跑补齐。

**部署**

每集群一个 Deployment（单副本），RBAC 只需 `ephemeralrunners: get/list/watch`（`base/monitoring-clusterrole.yaml` 已有）。它同时可以取代现有的 `runner-job-probe` CronJob——那个每 2 分钟全量 list 再 push 到 Pushgateway，是快照而不是事件流。

**与 GitCode 的关系（待定）**

GitCode 的 CI 跑在自建 runner 上，终态与 GitHub 用同一套 runner 集群，但由**另一个编排器**创建 pod（ARC 不可能服务 GitCode）。所以三期对 GitCode 的可行性取决于两件事：

1. 那个编排器把 run/job 身份写进 pod 的 label 或 annotation 了吗？
2. 它在 k8s 里以 CR 还是裸 Pod 存在？

有身份 → 复用归一事件模型，再加一个 watch 源即可；没有 → 集群侧只能到 Pod 粒度，对不上 run，那 GitCode 半边放弃集群侧深度。

### 4.4 核心流程

**流程一：一次普通 run**

```
GitHub/GitCode 创建 run
   │
   ├─ 全程 < 5 分钟：A（60s 内看到）→ 首页去重 → 计数 → 抓 /jobs 出 job 级
   │
   └─ 全程 > 5 分钟：B（5min 采样）进入 registry
          ├─ 每轮刷新在途 gauge（排队数、最久排队时长）
          ├─ 每轮对活跃 run 抓 /jobs → 最久 job 排队时长
          └─ 列表里消失 + 本轮请求成功 → GetRun 按 ID 核实 → 入账
```

**流程二：长任务不掉队**

GitHub 的列表按 `created_at` 倒序。实测 `vllm-ascend` 首页 100 条只覆盖 23 分钟——**创建后跑满 25 分钟以上的 run，完成时早已被挤出首页**，A 永远看不到。

所以长 run 的终态只能由 B 提供：它在还在跑的时候被采样进 registry，等它结束时按 ID 取回。这也是为什么 B 不是「可选的补齐」，而是长任务的主路径。

**流程三：重跑**

一期**接受缺口**：`Re-run` 不新建 run，只把 `run_attempt` 加一，`created_at` 不变，因此重跑旧 run 既不会出现在 A 的首页，B 也只在它恰好在跑且被采样到时才看得到。

影响是 `run_result_total` 少计这批结果，失败率在「依赖重跑救回来」的场景下偏高。三期由 event agent 修复：每次重跑都会新建 EphemeralRunner，事件必经。

**流程四：告警时延**

| 阶段 | 最坏 | 说明 |
| --- | --- | --- |
| 采集（A） | 60s | 轮询间隔 |
| 抓取 | 60s | 中心 `scrapeInterval` |
| 规则评估 | 60s | 未配 `evaluationInterval` |
| `group_wait` | 10s | Alertmanager |
| **合计** | **~3.2 min** | 不含 `for:` |

在途告警（排队过久、执行过久）走的是 B 的瞬时 gauge，**只需要「采样周期 + 抓取 + 评估」**：B 是 5 分钟一次，所以最坏约 7 分钟。对「排队 30 分钟」「执行 2 小时」这类阈值足够；要更快只能把 B 提到 60 秒，调用量翻 5 倍，不值。

**流程五：GitCode 接入**

同一套 aggregator，新增一个适配器：

1. `ListRuns` 把「已完成」拆成 `status=COMPLETED` 与 `status=FAILED` 两次调用（或本地过滤）；
2. `OrderKey` 用 `start_time`；
3. job 从 `stages[].jobs[]` 展开为平铺列表；
4. job 排队 = `start_time − apply_time`，`apply_time` 为 null 时跳过该 job 的排队样本；
5. 重跑与长任务补齐逻辑**先用实测确认是否需要**——GitCode 按 `start_time` 排序，长 run 不会因为新 run 创建而变老，原样照搬 GitHub 的补齐逻辑可能是多余的。

## 5. 已知边界

| 边界 | 影响 | 何时解决 |
| --- | --- | --- |
| 重跑旧 run 在一期看不到 | 失败率偏高 | 三期 |
| 失败原因只到 job 级 | 看不到哪个 step、哪条用例失败：REST `/jobs` 的 `steps[]` 只有名字与结论、无错误文本，用例级结果只在 job 日志里（要另拉 `/actions/jobs/{id}/logs`） | 需要时由单仓 push junit 上报，未列入本期 |
| `workflow_dispatch` 派发的回归只覆盖白名单 | 与"PR 验证"run 在 API 字段上不可区分，所以 L2′ 用 path 白名单收；白名单外的 dispatch 失败（如手工发布）只上大盘 | 上游仓补 `schedule` 触发后自然解决 |
| 平台自动添加的 workflow 也在采集范围内 | `Scorecard supply-chain security`、`pages-build-deployment` 等由 GitHub 自动创建，失败与我们的代码无关却会进 L2。实测 `verl` 的 2 趟破损里有 1 趟就是 Scorecard | 加入 3.1 的排除清单；先在大盘上看一段时间再定清单 |
| 改 workflow 文件名会换 series | `workflow` 标签取 `run.path`，改名即换身份，旧序列留在历史里不消失；改名为同一原因还会拆成两条 series | 接受（改名本身就是换身份）；真有批量改名时在 aggregator 侧配 `repo → workflow 别名` 映射 |
| PR 失败只有"整体崩塌"一种告警 | 单个 PR 的 CI 红不告警，靠 GitHub 原生通知到作者；L4 只抓 1 小时失败率 ≥80% 且样本 ≥10 的情形 | 如需"PR 卡住"提醒，走大盘或 aggregator 报表，不进 Prometheus |
| GitCode 的 run 级排队状态未验证 | 在途排队告警可能只有 GitHub 侧有效 | 接入 GitCode 前实测 |
| ghalistener 只覆盖 GitHub | GitCode 半边没有容量指标，也没有 ②+③ 启动耗时 | 取决于 GitCode runner 形态 |
| 非 ARC 执行的 run 不在集群侧视野 | event agent 只覆盖自建 runner | 接受 |
| aggregator 是单点 | 挂了就全量失明 | 用 2.3 的自监控兜底 |
| `total_count` 不可信 | 不能做精确对账 | 已改为四个守卫 |
