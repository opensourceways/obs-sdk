# CI 可观测目标（评审稿）

> **需求来源**：[backlog#1874](https://github.com/opensourceways/backlog/issues/1874)（26H2 CI 工作专项 · 里程碑 2）、
> [backlog#2080](https://github.com/opensourceways/backlog/issues/2080)（CI 质量问题可自助程度提升）、
> [backlog#296](https://github.com/opensourceways/backlog/issues/296)（让 ARC 失败的日志更加明确）
>
> **姊妹篇**：[微服务可观测目标](微服务可观测目标.md)——那份管 26 个微服务，本文管 CI 平台自身。
> 两份**共用同一套判定思路**（分层 / 最小必须集 / 「未知」是一等状态），但**对象、埋点位置、出口**都不同，不要互相套用。
>
> **本文覆盖两条 CI 线**（见 §0）。两条线共用同一套判定思路，但**各有各的四问与目标指标**——线 A 的关注点是"等多久、资源值不值"，
> 线 B 的关注点是"结果可不可信、有没有大面积挂"。**两套指标不要互相搬**。
>
> **本文只定目标与判定口径。** 改哪个仓、用什么组件、怎么自愈，留到各自的架构设计阶段（#2080 已按 need_design 立项）。

---

## 0. 两条 CI 线

社区里的 CI 不是一个东西，是**两条并行、技术栈完全不同**的线：

| | **线 A：参与型社区** | **线 B：主导型社区** |
| --- | --- | --- |
| **代表** | vllm（`vllm-ascend` / `vllm-project`）、sglang（`sgl-project`）、verl、triton（`triton-lang`）等 | **MindSpore**、openEuler、src-openeuler、openeuler-test |
| **代码托管** | GitHub | **GitCode** |
| **CI 引擎** | GitHub Actions + **ARC 自托管 runner** | **Jenkins 门禁**（`community_check_v2`）；少数仓库已迁 GitCode Actions / 华为云 CodeArts |
| **提交触发** | `on: pull_request` | webhook → ci-bot → Jenkins job |
| **结果回写** | GitHub Check / commit status | **ci-bot 标签** `ci_processing` → `ci_successful` / `ci_failed` |
| **稀缺资源** | **NPU 卡**（贵、按卡时计价） | **CPU 构建机 / 净室环境**（数量与并发槽位） |
| **用户最痛的** | **等太久**（拿不到卡） | **结果不可信、看不到为什么挂** |
| **运维最痛的** | 卡不够用 **+ 卡被占着不用** | **大面积集中失败没人先发现** |
| **资源信号的方向** | **下限**——利用率 > 30%，防「占着不用」 | **上限**——排队时长 / 队列深度，防「不够用」；**利用率低不是问题** |
| **已有验收标准** | #1874：排队 P95 < 10 min、卡时利用率 > 30% | **无**（#1874 / #2080 现有指标全是线 A 口径） |

### 0.1 为什么不能互相套用

- **两条线都有资源维度，但关心的方向相反。** 线 B 的算力是 **CPU 构建机 / 净室环境**（`check_build` 要按 x86-64 + aarch64 双架构构建 RPM，src-openeuler 是千级仓库），
  **不是"没有资源问题"**。差异在**信号方向**：线 A 盯**利用率下限**（NPU 贵，"占着不用"是首要浪费，要治）；
  线 B 盯**排队上限**（构建机不够用才痛，夜里利用率低是正常现象，不是问题）。
  所以 #1874 的「卡时利用率 > 30%」搬不过来——**不是因为没有资源，而是因为对线 B 来说"低利用率"不是要治的事**。
  线 B 若真要量资源，看的是 J1 的**排队段**，不是利用率。
  至于**卡型规格 / NPU 预检查**这类具体维度，在门禁上确实无对应物，强行套会产出一堆恒为 0 的指标。
- **线 B 的"结果可信度"是线 A 不痛的**：GitHub Actions 的成败由平台保证，用户不会怀疑"这个绿灯是不是假的"；
  而 Jenkins 门禁**会因为基础设施原因返回不了结果**（见 §2.2 的 `build_exception`），
  此时用户拿到的是一个**既不红也不绿的悬空状态**——这是线 B 独有的、也是国庆事故暴露的那一类问题。
- **判定点位置不同**：线 A 的状态天然落在 GitHub 侧（可查 API）；线 B 的状态散在「ci-bot 标签 / Jenkins job / 误报标记」三处，**没有任何一处是完整的**。

因此本文按线分章：**§1 线 A，§2 线 B**，共用 §3 的「面向用户的出口」与 §4 的边界说明。

---

## 1. 线 A：参与型社区（GitHub Actions / ARC）

### 1.1 目标：CI 可观测要回答的四个问题

CI 是把代码变成"能合入的结论"的生产线。它的"可用性"和微服务不一样——**没有 5xx，用户照样在等**。
所以目标不能照抄"错误率 < 1%"，得从**用户和运维实际会问的四个问题**倒推：

| # | 在问什么 | 现在能不能回答 | 目标口径（可量化） |
| --- | --- | --- | --- |
| Q1 | **还要等多久？** | ⚠️ 分布**已有**（`gha_job_startup_duration_seconds`，45 桶），但**没人算分位数**；"段一"零信号 | **排队时长**（提交 → 开始执行）可查，P95 **< 10 min** |
| Q2 | **这次跑得怎么样？** | 半个——只有"超过 2.5 h"的点告警 | **执行耗时** P95 **< 10 min**；**成败**可按仓库 / 规格 / 用户下钻 |
| Q3 | **资源用得值不值？** | 节点 / 卡维度能看，但**算不出"使用率"这个数** | **NPU 卡时利用率 > 30%**，并能识别"占着不用" |
| Q4 | **出问题谁先知道？** | 运维先知道（邮件），用户更晚 | **提前于用户识别**；用户可自助的先提示用户 |

> Q1 / Q2 的两个 10 min、Q3 的 30%，直接来自 [#1874](https://github.com/opensourceways/backlog/issues/1874) 的验收标准，不是本文新提的指标。

**核心判断**：**线 A 的重心不在"基础设施活着没有"，而在"这一单交易（一次 workflow run）等了多久、跑成什么样、资源花得值不值"。**
前者已经做得很厚（§1.2），后者是本节要补的那一层。

### 1.2 现状：基础设施层已跑通一套闭环，缺的是作业层

`ascend-ci-deployment/monitoring/` 与微服务方案**同构**，且**已经在跑**：

```
各 CI 集群 ─┬─ Prometheus Agent（scrape kube-state-metrics + node-exporter）
            │                                  │ remote_write
            └─ 拨测 CronJob ──► Pushgateway ───┤
                                               ▼
                                  中心 Prometheus（infra-cn4-x86-common-cluster · ns infra-monitoring）
                                               ├─► Grafana（ARC 官方大盘 + sglang 大盘）
                                               └─► Alertmanager ──► 邮件
```

| 已有之物 | 内容 |
| --- | --- |
| **拨测 9 类** | `github-probe`（5 min，直连 + gh-proxy 双路）、`github-status`（GitHub 官方状态页）、`shared-disk`（10 min）、`cloud-account`（每小时余额）、`sa-audit`（每小时）、`cert-expiry`（每天）、`mirror-sync`（30 min）、`runner-version`、`sfs-turbo-disk` |
| **告警 31 条** | 见下 |
| **大盘** | **ARC 官方大盘**（`manifests/grafana/grafana-dashboard-arc.yaml`，约 25 个面板：Runner Performance / Startup Duration / Job Execution / Pending Runners / Out of Memory / Queue Depth / Reconcile Time …），另有一个 sglang 业务大盘 |
| **排队相关指标** | `gha_job_startup_duration_seconds`（**histogram**，每个 scale-set 显式配了 45 个桶，0.01 s → 3600 s）、`gha_controller_pending_ephemeral_runners`（gauge）、`gha_running_jobs` / `gha_busy_runners` / `gha_idle_runners` / `gha_desired_runners`（均带 `repository` / `organization` 等 label） |

现有 31 条告警按**判定对象**分四类：

| 类别 | 代表告警 |
| --- | --- |
| **外部依赖** | `GitHubUnreachable`、`GitHubProxyUnreachable`、`GitHubDirectUnreachable`、`GitHubStatusOutage`、`CloudAccountLowBalance`、`CertExpiring`、`SharedDiskHighUsage`、`SharedDiskMountFailed`、`SAAuditAPIUnreachable` |
| **CI 组件自身** | `RunnerCrashLooping`、`RunnerPodPendingTooLong`、`RunnerImagePullFailed`、`RunnerInvalidImageName`、`InfraServiceCrashLooping`、`ListenerCrashLooping`、`NginxPodDown`、`NPUCardDropped` |
| **拨测自身的可信度** | `ProbeStale`、`GitHubProbeMissing`、`GitHubStatusProbeStale`、`SfsTurboDiskProbeStale`、`CertProbeFailed`、`GitHubStatusPageUnreachable` |
| **作业 / 合规** | `WorkflowRunTooLong`（超 2.5 h）、`RunnerUpdateAvailable`、`RunnerVersionExpiring`、`SAExcessiveClusterAdmin`、`AlertDeliverySLOBreach` |

> **第三类是这套东西做得好的一点**：它特意为"拨测本身死了"配了告警（`ProbeStale` / `GitHubProbeMissing`），
> 否则"探不到"和"没在探"长得一样。这正是姊妹篇里"「未知」是一等状态"的同一条原则——**线 B 要把这条抄过去**（§2.4 J5）。

**缺的是「作业层」**：

| 层 | 看什么 | 现状 |
| --- | --- | --- |
| **资源层** | 节点 / NPU 卡 / 磁盘 / 网络 | ✅ node-exporter + kube-state-metrics，已有 |
| **控制器层** | ARC 的 runner 伸缩、队列、reconcile | ✅ ARC 自带指标 + 官方大盘，已有 |
| **作业层** | **一次 workflow run 的等待、耗时、成败、归因** | ⚠️ 排队时长**有分布数据**（没算分位数）、另有两条"超长"点告警；**段一零信号**、无下钻维度、无用户出口 |

作业层的三个具体缺口：

1. **"等待"有数据，但没有判定，而且只覆盖一半。** 排队时长**已经有 histogram**——`gha_job_startup_duration_seconds`，
   每个 scale-set 显式配了 **45 个桶（0.01 s → 3600 s）**，大盘上也有 `Startup Duration` 面板。缺的是两端：
   **没人算分位数**（面板只做 `sum by(le) (increase(..._bucket))`，全仓无一处 `histogram_quantile` 作用在它上面，
   所以 #1874 的"P95 < 10 min"是**数据齐了、判定没做**）、**"段一"完全没信号**（见 §1.3 末）。
   另外，唯一一条量"等太久"的告警 `RunnerPodPendingTooLong` 是 **`for: 35m`**，而目标是 P95 < 10 min——
   **它响的时候 SLO 已被超 3.5 倍**；它数的是"pod 已创建但还没被调度到节点"，用途是防卡死，不是测排队。
2. **没有下钻维度。** 现有告警的 label 是 `cluster` / `namespace` / `pod`，这是**运维视角**（哪个集群坏了）。
   用户问的是"**我这个仓库为什么慢**"，需要 `repo` / `runs-on` 规格 / 触发者这些维度，现在一个都没有。
3. **出口是运维，不是用户。** 告警路由到的是**逐个列出的运维个人邮箱**（`alertmanager-config-secret.yaml`），
   没有任何面向社区用户的出口。用户唯一的感知途径是"job 一直不开始"。这正是 #2080 / #296 要改的（§3.1）。

### 1.3 目标指标：最小必须集 C1–C5

沿用姊妹篇的"四问法"，但**从一次 workflow run 的生命周期倒推**，而不是从 CI 组件有什么函数倒推：

| # | 问题 | 产出的信号 | 对应目标 |
| --- | --- | --- | --- |
| **C1** | 用户提交后，**等了多久才开始**？ | 排队时长 Histogram + 队列深度 Gauge | Q1 |
| **C2** | **跑成什么样、跑了多久**？ | 作业 Counter（带 `result` 区分成败）+ 耗时 Histogram | Q2 |
| **C3** | 资源**忙不忙**？ | 每资源池的"卡时忙 / 卡时可用" seconds Counter | Q3 |
| **C4** | 失败**卡在哪一阶段**？ | 阶段维度的失败 Counter（见下） | Q4 / 自助提示 |
| **C5** | 每条链路上**至少一个无条件 set 的 gauge** | — | 防"全静默 = 全成功" |

> **C1 是线 A 的重点**——不是因为它"现在完全没有"（分布数据已有，见 §1.2），而是因为**验收标准要的那个数（P95）现在没人算**；
> 且它天然跨两条链路（段一在 GitHub 侧、段二在集群侧），比 C2–C5 都难凑齐。
> **C5 沿用姊妹篇的教训**：Counter 在没有事件时是**静默**的，"今天一次都没排队"和"排队指标根本没埋"在图上长得一样。
> 所以每条关键链路上至少要有**一个无条件 set 的 gauge**（如"当前队列深度""当前 pending runner 数"）来证明这条链路还活着。

**C4 的阶段划分**——不新造，是把已有那批"CI 组件自身"告警的判定对象，重新组织成用户能读的维度：

```
调度 → 镜像拉取 → Pod 启动 / runner 注册 → NPU 预检查 → 作业执行
```

**一个必须点破的取数盲区**：GitHub Actions 的"排队"有**两段**——

- **段一：job 已在 GitHub 侧排队，但还没有 runner 被分配。** 此时集群里**什么都没有**，本地零信号。
- **段二：runner 已被分配，但作业还没真正跑起来**（等调度 / 等镜像 / 等 runner 注册）。

**段二的分布已经有了**——`gha_job_startup_duration_seconds` 就是为它埋的 histogram（桶一直配到 3600 s，说明设计时就预期它会很长）。
而 **#1874「排队 < 10 min」的痛点主要在段一**，段一**只能从 GitHub 侧取数**（API / webhook），本地无论如何也量不到。

> 这条不是推测：`arc-fedsched` 的 `wait_from_arrival_seconds` 注释直接写着 ——
> *"Seconds from arrival to the release；**equal to `wait_seconds` until a GitHub queue time exists**"* ——
> 连未来那套调度器也把"GitHub 侧的排队时间"当成一个**外部输入**，拿不到就先退化成"从到达算起"。
> 这是 §5 待确认项 1 的由来。

顺带纠一个容易误解的面板名：ARC 官方大盘里的 **`Queue Depth` / `Workqueue Queue Duration` 不是用户 job 的排队**，
它们是 `workqueue_depth` / `workqueue_queue_duration_seconds`——**控制器自己的 reconcile 队列**（controller-runtime 标准指标）。
真正对应用户排队的是 `Startup Duration` 面板，以及 `Pending Runners`（`gha_controller_pending_ephemeral_runners`）。

### 1.4 质量问题分类与自助程度分级（线 A）

[#2080](https://github.com/opensourceways/backlog/issues/2080) 已定三档（下表右列的目标值引自其验收标准）：

| 档 | 判定 | 出口 | 目标 |
| --- | --- | --- | --- |
| **① 已知可自愈** | 故障模式已识别，且有确定性修复动作 | **集群内自动修复**（重启 / 重建 / 驱逐） | 自愈成功率 ≥ 80% |
| **② 用户可自助** | 由用户输入导致，用户自己能改 | **GitHub 侧主动提示 + 修复建议** | 无需 SRE 介入 |
| **③ 需人工** | 需要 infra 判断 | **结构化错误信息（错误码 + 原因 + 建议措施）** + SOP | SRE 重复介入 -60% |

**归到哪一档不是拍脑袋，而是看"错在 C4 的哪一段"**：

| C4 阶段 | 典型故障 | 初判档位 |
| --- | --- | --- |
| 调度 | `runs-on` 标签写错、请求卡数超过机型上限 | ② 用户可自助 |
| 调度 | 整个资源池没卡了 / 跨集群（karmada）层问题 | ③ 需人工 |
| 镜像拉取 | 临时 registry 抖动 | ① 可自愈 |
| Pod 启动 | runner pod crashloop | ① 可自愈 |
| NPU 预检查 | 作业要求的环境不满足（如 error 8020） | ② 用户可自助 |

> ⚠️ 上表是**待评审的初判**，不是结论。#2080 的架构设计阶段会逐条过；本文只提供**分类维度**。
> **线 B 要另过一遍**——它的故障面完全不同（见 §2.5）。

---

## 2. 线 B：主导型社区（GitCode + Jenkins 门禁）

### 2.1 目标：线 B 要回答的四个问题

线 B 的痛点和线 A **不重叠**：用户不会怀疑"门禁在偷偷占着贵卡"（没有卡），痛的是**结果可不可信、挂了看不看得懂**。
但它**同样会排队**——构建机不够用、Jenkins 执行器被占满，PR 一样要等，所以 Q1 仍然是"多久出结果"。
差别落在 Q3：线 A 问的是"贵卡用得值不值"，线 B 问的是"**这个结论是不是真的**"。
所以四问重新倒推：

| # | 在问什么 | 现在能不能回答 | 目标口径 |
| --- | --- | --- | --- |
| Q1 | **我的 PR 门禁多久出结果？** | ❌ 只有"超时"这类点告警，没有分布 | 门禁**总时长**（提交 → 出结论）P50 / P95 可查、可按仓库下钻 |
| Q2 | **挂了——挂在哪个检查项？** | ❌ 只有红/绿标签，原因要人工翻 Jenkins 控制台 | 成败可按**检查项**（`check_*` / `compare_package`）下钻；失败**有结构化原因** |
| Q3 | **这个结论可信吗？** | ❌ 误报只能事后人工打 `/ci_mistake` | **误报率**可算（由 `/ci_mistake` 反推）；**结果未返回率**（`build_exception`）可算且**单独告警** |
| Q4 | **现在整体健康吗？** | ❌ **零**——这正是国庆事故那一处 | **大面积集中失败在用户报障前被发现**（滑动窗口成功率跌破阈值即告警） |

> **Q3 / Q4 是线 B 的两个"只有这条线才有"的问题。**
> Q3 的来源：门禁会因基础设施原因**返回不了结果**——用户拿到一个既不红也不绿的悬空状态，只能反复 rebase 或求人；
> 而现有体系对"结果没回来"和"结果回来了且通过"**没有任何区分**。
> Q4 的来源：backlog#1938 最后一条评论的结论原话——「**缺少根据最终结果性判断，比如出现大批量集中失败**」。

**核心判断**：**线 B 的重心不在"快不快"，而在"这个结论是不是真的、有没有整体性崩塌"。**
它要防的是**静默**——门禁集体失败而没人知道，比门禁慢几个小时严重得多。

### 2.2 链路与判定点

| 环节 | 实体 |
| --- | --- |
| 托管 | GitCode（openEuler / src-openeuler / openeuler-test 三个 org） |
| 门禁入口 | Jenkins `https://openeulerjenkins.osinfra.cn/job/Infra/job/community_check_v2/` |
| 门禁代码 | `openeuler/openeuler-jenkins`（按仓四类 job：`trigger` / `x86-64` / `aarch64` / `comment`） |
| 状态回写 | ci-bot 标签 `ci_processing` → `ci_successful` / `ci_failed` |
| **失败出口** | **人工深入 Jenkins 构建控制台** |
| 误报标记 | `/ci_mistake build_no <mistake_type> <ci_mistake_stage>` |

> ⚠️ **这张表只覆盖 openEuler / src-openeuler 一侧。** 线 B 里 **MindSpore 是另一套实现**——独立 Jenkins 实例
> （`mindspore-jenkins`，源码仓 `opensourceways/mindspore-jenkins`，ArgoCD app `mindspore-jenkins-master`；
> 日志入口 `build-log.mindspore.cn`），门禁 job、检查项、状态回写方式都不一定与 `community_check_v2` 相同。
> **这不是细节**：线 B 内部**至少有两个不同的门禁实现**，所以判定对象必须是"**一次门禁**"而不是"一次 Jenkins build"
> （§2.3 末的同一条结论）。MindSpore 侧的具体链路本文未展开，待补。

**检查项清单**（这是线 B 的"阶段"维度，等价于线 A 的 C4 阶段）：

| 社区 | 检查项 |
| --- | --- |
| **src-openeuler** | `check_binary_file`、`check_package_license`、`check_package_yaml_file`、`check_spec_file`、`check_consistency`、`check_build`、`check_install`、`compare_package`（+17 个 interface-change 子项：add_rpms、delete_rpms、kabi、drive_kabi、kconfig、ko、rpm_files、rpm_provides、rpm_requires、rpm_abi、rpm_jabi、rpm_cmd、rpm_config、rpm_header、rpm_service、rpm_lib、rpm_symbol） |
| **openEuler** | `check_code`（→ majun / openlibing）、`check_sca`、`check_package_license`、x86-64 / aarch64 构建 |

**webhook 链路**（本地可查部分）：

```
GitCode PR 事件 ─► robot-universal-hook-delivery ─► Kafka metadata_webhook_gitcode
                                                        │
                                                        ▼
                                    robot-hook-dispatcher ─► robot-universal-access ─► 业务机器人
```

> ⚠️ **链路里断了一截，这是线 B 第一个要补的洞**：`community-robots` 全仓 grep `jenkins` **零命中**——
> **没有任何机器人读 Jenkins 结果，也没有任何机器人给 GitCode 写 commit status**。
> 标签 `ci_successful` / `ci_failed` 是谁写的、从哪取的结果，**本地看不到**。
> 不补上这一截，Q2 的"结构化失败原因"和 §3.2 的用户出口都无处安放。

### 2.3 现状：已有四件制品，但全是离线批处理看板；监控接入为零

**必须先说清楚：这条线不是空白。** 已经有相当多制品：

| 制品 | 是什么 | 性质 |
| --- | --- | --- |
| **`openeuler_cicd_dashboard`** | GitCode 元数据 + PR 门禁构建记录 + Jenkins 数据。组织概览（openEuler / src-openeuler 双 tab、7 / 14 / 30 天筛选、最近构建 / 平均 / 中位 / P90 / 成功率）；单仓详情（门禁通过率环形图、**Jenkins 排队等待 + 各阶段耗时**、`check_code` / `check_sca` 徽章、`compare_package` 窗口）；Night 版本级流水线。原生 HTML/CSS/JS + Python 标准库，零依赖 | **离线批处理看板** |
| **门禁结果与误报看板** | `openlibing-beta.osinfra.cn/develop/incrementView` | 看板 |
| **`PipeLine_DashBoard`** | gitcode.com/yejianping/PipeLine_DashBoard，Flask；GitCode MR + Jenkins JSON API；数据模型 Pipeline / Stage / MergeRequest / MRBuildInfo | 看板 |
| **`jenkins-log-viewer`**（[backlog#1144](https://github.com/opensourceways/backlog/issues/1144)） | MindSpore 的 `build-log.mindspore.cn`（测试环境 `jenkins-log-viewer.test.osinfra.cn`）；Go+Gin 代理 Jenkins REST（`/api/build/info`、`/api/build/log`、`/api/build/list`、`/api/healthz`）。**明确声明「不接触 GitCode」**，PR-Jenkins 关联由机器人在 PR 评论里贴 URL | 日志跳转入口 |

> `openeuler_cicd_dashboard` 的细节来自公开索引，**未能直接抓取**（`raw.gitcode.com` 403、`api.gitcode.com` 401 需 token），引用前建议核一遍。

**但这四件都是「人去看的看板」，不是「会自己叫的监控」。** 具体空白：

| # | 空白 | 实测依据 |
| --- | --- | --- |
| **B-1** | **作业层指标完全缺失**——没有任何脚本取 build 的 `result` / `duration` / `queue`。本地仅有的两个 Jenkins 采集脚本只服务于**镜像-源码仓映射**，无成败、无耗时、无排队 | `infrastructure/scripts/fetch_jenkins.py`（只取 `lastBuild[timestamp]`）、`scan_jenkins_logs.py`（只取 `lastSuccessfulBuild[number]` + `consoleText`） |
| **B-2** | **Jenkins 从未被监控**——没装 prometheus 插件，任何 Prometheus 都没抓它 | `infra-common/common-applications/control/jenkins/deployment.yaml`；`openeuler-cn-north4-jenkins/monitor-agent/prometheus.yml` 的 scrape job 只有 `kube-state-metrics` / `prometheus-agent` / `services` |
| **B-3** | **门禁 Jenkins 所在集群零监控栈**——六条清单里没有 Prometheus / Grafana / exporter / 告警任何一件 | `infra-community/helm-charts/openeuler-ci-cn4-cluster/`（仅 `jenkins.yaml` + `project.yaml`；清单 = jenkins、jenkins-data-scan-all、oauth2-proxy、rsync-client、rsync-server + build-02 ArgoCD） |
| **B-4** | **无一条告警面向门禁**——现有 31 条全部面向 ARC / GitHub | `ascend-ci-deployment/monitoring/config-for-infra-cn4-x86-common-cluster/prometheus-rules.yaml` |
| **B-5** | **无门禁大盘**——现有大盘是 ARC 官方大盘、robot-review 健康度、社区健康度 | `helm-chart-value-osw/common/grafana/prod/values-dashboards.yaml` |
| **B-6** | **无归因 / 自愈 / SOP**——失败分类、"用户可自助"提示、SOP 全部为零（线 A 侧已有 32 桶归因 + 三档设计） | `general/昇腾失败原因分析/`（**全部针对 GitHub Actions + NPU，没有一条针对 GitCode 门禁**） |
| **B-7** | **「门禁大面积失败」检测能力为空** | backlog#1938 最后一条评论 |
| **B-8** | **构建侧容量完全不可见**——门禁要按 x86-64 / aarch64 **双架构构建 RPM**（另有净室环境），但构建机数量、执行器占用、排队长度**一个数都没有**；"构建机不够用了"只能靠用户报障 | B-3：门禁集群连 node-exporter / kube-state-metrics 都没有 |

**另有一个机制与事故根因链直接相关，值得一并纳入：**

**净室构建出站 IP 白名单** = `jenkins-log-scanner` 的 `vpc_ip_whitelist_auto_resolver`
（subfinder + 并发 DNS → 华为云 VPC 安全组地址组），按社区部署为 ns `infra-security` 的 CronJob（每小时），**无任何监控**。
它的语义是**只增不减**——"白名单里只有旧 IP"这种现象只有两种可能：**要么域名不在它的域名清单里，要么它自己没跑成而无人知道**。
两种都能被一条最朴素的"resolver 最后成功时间"gauge 覆盖，而现在一条都没有。**这是为什么那次只能人肉发现。**

> **可见的迁移方向要修正一个说法**：不是"Jenkins → GitCode Actions"，而是 **Jenkins → 华为云 CodeArts**
> （`robot-universal-quality-gate-trigger` 的设计目标就是触发 CodeArts 流水线，且业务逻辑尚未实现）。
> 代码托管的迁移方向才是 Gitee / AtomGit → GitCode。
> **这条决定线 B 的指标不能焊死在 Jenkins 上**——判定对象应该是"**一次门禁**"，而不是"一次 Jenkins build"。

### 2.4 目标指标：最小必须集 J1–J5

与线 A 一一对应（"阶段维度"换成"检查项维度"，"卡时利用率"换成"排队段 + 结果可信度"）：

| # | 问题 | 产出的信号 | 对应目标 |
| --- | --- | --- | --- |
| **J1** | 从提交到出结论**多久**？ | 门禁时长 Histogram（**排队段 + 执行段分开**，排队段是 Jenkins `queue` 时间）+ 队列深度 Gauge | Q1 |
| **J2** | 结果**是什么**、卡在**哪个检查项**？ | 门禁结果 Counter（带 `result`）+ **检查项维度**的失败 Counter | Q2 |
| **J3** | 结果**可信吗**？ | 误报 Counter（由 `/ci_mistake` 反推）+ **结果未返回 Counter**（`build_exception`） | Q3 |
| **J4** | 整体**崩没崩**？ | 「窗口内成功率」记录规则 + 告警（环比基线跌破即响） | Q4 |
| **J5** | 每条链路上**至少一个无条件 set 的 gauge** | — | 防"全静默 = 全成功" |

**J3 是线 B 的重点，也是线 A 完全没有的东西。** 它要单独成一个信号，不能混进 J2 的 `result`：

```
门禁结果三态（而不是二态）：
  success      ── 结果返回了，且通过
  failure      ── 结果返回了，且不通过
  no_result    ── 结果没返回 ← 现在既不算红也不算绿，无人知晓
```

`no_result` 对应的就是 `/ci_mistake` 里的 `build_exception`——**用户被迫用"打误报标记"这个人工动作来告诉运维"门禁没出结果"**。
把 `build_exception` 的**标记量**当成一个可观测信号（它本身就是用户侧上报的故障率），是这条线上最便宜的一个起点。

**J4 的形态**（线 A 没有、线 B 必需）：不看单个 job，看**窗口内的整体**——

```
rate(门禁结果[15m]) 按 result=failure / 总体      ── 跌破基线即告警
       或
最近 N 分钟内 no_result 占比                      ── 超过阈值即告警
```

> 这正是"大批量集中失败"能被自动发现的最小实现。**注意它是记录规则（recording rule）+ 告警，不是一个 Counter**——
> 单条 Counter 永远看不出"集中"，必须有一个按窗口聚合的视图，才有资格叫"结果性判断"。

**两个取数盲区（对应线 A §1.3 的"两段排队"）：**

1. **`no_result` 需要一个"预期有结果"的分母。** 光看出 `no_result` 计数不够——还得知道"本该有多少次门禁"。
   这依赖 §2.2 那条断掉的链路（谁能写出 `ci_processing` 标签，谁就更接近这个分母）。
2. **排队段可能取不到。** 如果 Jenkins 侧不暴露 queue 时间（B-2 里它连 `/metrics` 都没开），
   J1 的"排队段"只能退化为"从 webhook 收到事件到 job 开始"——**需要先确认事件时间戳在链路哪一环可查**。

**一个必须提前定的高基数问题**：`repo` 能不能当 label？

- src-openeuler / openEuler 的仓库数量在**千级**，直接当 metrics label 会明显抬高时序基数。
- **建议**：metrics 侧只到 `org` + `check_type` + `result` 这些低基数维度；
  **`repo` 级的下钻落到看板 / 日志查询**（`openeuler_cicd_dashboard` 那类离线看板正好补这个位置），**不进 metrics label**。
  这条如果定了，线 B 与线 A 的分工也就清楚了：**metrics 管"整体崩没崩"，看板管"是哪个仓库"。**
- 这是 §5 待确认项 3。

### 2.5 自助分级按检查项重新过一遍（线 B）

线 A 的 §1.4 三档表**不能直接搬**——它的故障面是 NPU 作业，线 B 的是 RPM 包与合规检查。
按 §2.2 的检查项重列（同样是**待评审的初判**）：

| 检查项 | 典型故障 | 初判档位 |
| --- | --- | --- |
| `check_package_license` / `check_spec_file` / `check_package_yaml_file` | spec / yaml 写法不合规 | ② 用户可自助（给出具体行号与规则） |
| `check_consistency` | 与上游版本不一致 | ② 用户可自助 |
| `check_binary_file` | 提交了二进制文件 | ② 用户可自助 |
| `compare_package`（17 子项） | kabi / rpm_requires / ko 等 interface 变更 | ③ 需人工（**但要给出结构化 diff**，否则用户完全无从下手） |
| `check_code` / `check_sca` | 代码扫描 / 开源合规告警 | ② 用户可自助（指向 majun / openlibing 报告） |
| **任意检查项 + `no_result`** | **门禁结果没返回**（净室白名单、构建机资源、Jenkins 自身） | ③ 需人工 + **① 可自愈**（能自愈的先自愈，且必须通知用户"重跑即可"） |

> 最后一行的分叉是线 B 特有的一档：**同一个检查项，`failure` 归用户，`no_result` 归基础设施**。
> 不做这个分叉，"门禁挂了"会被当成"你的包有问题"派给用户——这是国庆那类事故里最伤用户信任的一种表现。

---

## 3. 面向用户的出口

**线 A 和线 B 在这一点上现状完全相反**：线 A 有形态设计但路由错了人；线 B 是**真正的零**。

### 3.1 线 A：把失败原因写回 GitHub Check

现状：**所有告警的收件人都是运维个人邮箱**，社区用户拿不到任何结构化信息。

目标形态（[#296](https://github.com/opensourceways/backlog/issues/296) 已给出）：

```
PR Checks
└── ❌ self-hosted-runner/k8s-startup
    ├── title:   Runner pod failed before job started
    ├── summary: FailedScheduling: insufficient npu.com/Ascend910B
    ├── details: workflow / job / runner scale set / namespace / pod / node
    └── link:    内网 Grafana / 事件页
```

### 3.2 线 B：GitCode PR 状态（现状为零）

现状（**这是线 B 最干净的一个空白**）：

- **没有任何服务把 Jenkins 构建结果 / 失败原因回写到 GitCode PR。**
  `community-robots` 全仓 grep `jenkins` 零命中；GitCode PR 上的评论只有人工审查 checklist、标签、keeper 门禁信号。
- 唯一接近的出口是 **`jenkins-log-viewer` 的跳转链接**——机器人在 PR 评论里贴一个 URL，用户自己点进去看。
  **这是"入口"不是"摘要"**：不点开就不知道挂了什么，且它明确声明"不接触 GitCode"。
- 失败原因**仍然需要人工深入 Jenkins 控制台**。

目标形态（与线 A 的 Check 同构，落到 GitCode 的实现待定）：

```
GitCode PR
└── ❌ 门禁 · src-openeuler
    ├── 检查项:   compare_package / rpm_requires
    ├── 结论:     不通过（结构化的 3 条差异）
    ├── 归因:     ② 用户可自助 / ③ 需人工 / ① 可自愈
    └── 链接:     本检查项的 Jenkins 日志（复用 jenkins-log-viewer）
```

> **这条出口是两条线 §1.4 / §2.5 三档能落地的前提**：没有面向用户的出口，分档分得再细，用户也感知不到。

---

## 4. 与《微服务可观测目标》的边界

| | 微服务可观测 | CI 可观测（本文） |
| --- | --- | --- |
| **对象** | 26 个微服务仓 | CI 平台（**线 A**：ARC 资源池 + 调度 + 周边工具链；**线 B**：GitCode 门禁 + Jenkins + 误报体系） |
| **埋点位置** | obs-sdk（应用内） | 集群侧 / 门禁侧（Prometheus Agent / 拨测 / ARC / 未来的 dispatcher / 门禁 adapter） |
| **数据落地** | 本方案**新建**：`infra-cn4-x86-common-cluster` · ns `infra-monitoring-community` | **线 A**：CI 自己已有，同集群 · ns **`infra-monitoring`**；**线 B**：**目前无处可落**（B-3：门禁集群零监控栈） |
| **出口对象** | community 用户（社区服务状态页） | CI 使用者（GitHub / GitCode 侧）+ 运维 |

**线 A 的两套监控栈在同一集群、不同 ns，且已明确决定不合并**（技术设计 §4.8.1 给了三条理由：ArgoCD 共管打架、资源块的 Prometheus 占着共享 ELB、不想碰它的 Operator 与 CRD）。
所以**"中心要不要并"这个问题已经有答案了**，本文不重开；**剩下的真问题是「大盘要不要统一成一个入口」**——现在至少存在 ARC 官方大盘、sglang 大盘、robot-review 健康度、社区健康度，本方案还会再建一个 Grafana（§4.9）。

> **线 B 给这个问题加了一档新难度**：它的门禁集群（`openeuler-ci-cn4-cluster`）**连监控栈都没有**，
> 所以线 B 要先回答"**指标落在哪**"（复用 ns `infra-monitoring`？还是给门禁集群也配一套 Agent？），
> 这比对"大盘要不要合并"的讨论更靠前。见 §5 待确认项 4。

---

## 5. 评审待确认项

| # | 待确认 | 建议 | 责任方 |
| --- | --- | --- | --- |
| 1 | **线 A · C1 段一（未分配 runner 的排队）怎么取数**（§1.3 末） | GitHub API / webhook 二选一；需确认速率限制与 token 权限。也可等 `arc-fedsched` dispatcher 就绪后由它统一给——它已带 `wait_seconds` / `queued_seconds_total` 等指标 | 待定 |
| 2 | **线 A · §1.4 三档分类的初判** | 由 #2080 架构设计阶段逐条过审，本文只给维度 | ascend-ci 项目 |
| 3 | **线 B · `repo` 是否进 metrics label**（§2.4 末） | 建议**不进**：metrics 只到 `org` / `check_type` / `result`，`repo` 级下钻交给看板。定了这条，线 B 的 metrics 与看板分工就清楚了 | 待定 |
| 4 | **线 B · 指标落在哪**（§4 末） | 门禁集群 `openeuler-ci-cn4-cluster` 无监控栈，三种选择：复用 ns `infra-monitoring` / 给门禁集群配 Agent / 先只在门禁侧做轻量 exporter。**这是线 B 的前置问题** | 基础设施组 |
| 5 | **线 B · `no_result` 的分母从哪来**（§2.4） | 需要确认"谁能写出 `ci_processing` 标签"——即 §2.2 那条断掉的链路归属哪个服务 | 待定 |
| 6 | **线 B · 门禁的无条件 gauge 埋在哪**（J5） | 连带把"净室白名单 resolver 最后成功时间"这类当前完全不可见的机制一并纳入（§2.3 末） | 待定 |
| 7 | **本文的落点拆解**：线 A 落在 `ascend-ci-deployment`（监控 / 告警 / 自愈）、`runner-container-hooks`（错误信息）、`ascend-runner-onboarding`（自动化）、`ascend-gha-runners/arc-fedsched`（调度指标）；**线 B 暂无对应仓** | 与 #2080 的工作量估算（约 15~20 人天，**只覆盖线 A**）分开排期，本文不做拆分 | 基础设施组 |
| 8 | **大盘是否统一入口**（§4 末） | 与 #668「社区服务状态页的实现形态」一并定；两者都是"视图层入口"问题 | 运维 |
| 9 | **线 B 要不要"构建机利用率"这类指标**（§0.1 / §2.4） | 建议**先不做**：线 B 的资源信号用 J1 的排队段就够，利用率对线 B 不是行动信号（低利用率 ≠ 有问题，可能只是夜间低谷）。**待确认的是"构建机到底够不够用"**——若确认是瓶颈，再补一个"执行器占用 / 排队"的上限类指标（不是利用率下限） | 待定 |

---

## 附：本文引用的实测依据

### 线 A

| 依据 | 来源 |
| --- | --- |
| 拨测 9 类、链路形态、集群清单 | `ascend-ci-deployment/monitoring/README.md`、`monitoring/base/cronjob-*.yaml` |
| 现有告警 31 条 | `ascend-ci-deployment/monitoring/config-for-infra-cn4-x86-common-cluster/prometheus-rules.yaml` |
| 告警只发运维个人邮箱 | `monitoring/config-for-infra-cn4-x86-common-cluster/alertmanager-config-secret.yaml` |
| ARC 官方大盘面板 | `ascend-ci-deployment/manifests/grafana/grafana-dashboard-arc.yaml` |
| 排队分布**已有**但未算分位数；`RunnerPodPendingTooLong` 为 `for: 35m` | `ascend-ci-deployment/projects/*/*/values.yaml`（`histograms: gha_job_startup_duration_seconds`，45 桶）、同上大盘的 `Startup Duration` 面板、`monitoring/config-for-infra-cn4-x86-common-cluster/prometheus-rules.yaml:734` |
| 段一需 GitHub 侧 queue time（非推测） | `arc-fedsched/pkg/metrics/metrics.go`（`wait_from_arrival_seconds` 的 Help 文案） |
| 两个 10 min / 30% 的验收标准 | [backlog#1874](https://github.com/opensourceways/backlog/issues/1874) 里程碑 2 与验收标准 |
| 自愈 ≥ 80% / SRE -60% / 三档 / 四个模块 | [backlog#2080](https://github.com/opensourceways/backlog/issues/2080) 验收标准与其需求分析说明书 |
| GitHub Check 的输出形态 | [backlog#296](https://github.com/opensourceways/backlog/issues/296) |
| dispatcher 将带来的排队 / 容量指标 | `arc-fedsched/pkg/metrics/metrics.go`（ns `arcfed`：`wait_seconds`、`queued_seconds_total`、`head_blocked_seconds_total`、`node_free_seconds_total`、`cluster_units_free` …） |
| 两套监控栈同集群不同 ns、不合并 | [微服务可观测性建设技术设计](微服务可观测性建设技术设计.md) §4.8.1 |

### 线 B

| 依据 | 来源 |
| --- | --- |
| 门禁架构、四类 job、误报标记语法、检查项清单 | `openeuler/openeuler-jenkins`；`openeulerjenkins.osinfra.cn/job/Infra/job/community_check_v2/` |
| 误报看板 URL | `openlibing-beta.osinfra.cn/develop/incrementView` |
| `openeuler_cicd_dashboard` 数据模型与指标（**来自公开索引，未直接抓取**） | GitCode `openeuler_cicd_dashboard`（`raw.gitcode.com` 403 / `api.gitcode.com` 401，引用前请复核） |
| `PipeLine_DashBoard` | gitcode.com/yejianping/PipeLine_DashBoard |
| `jenkins-log-viewer` 形态与"不接触 GitCode" | [backlog#1144](https://github.com/opensourceways/backlog/issues/1144) 需求分析与架构设计说明书 |
| B-1：采集脚本只取时间戳与日志 | `infrastructure/scripts/fetch_jenkins.py`、`infrastructure/scripts/scan_jenkins_logs.py` |
| B-2：Jenkins 无 prometheus 插件、Agent 不抓 Jenkins | `infra-common/common-applications/control/jenkins/deployment.yaml`、`.../openeuler-cn-north4-jenkins/monitor-agent/prometheus.yml` |
| B-3：门禁集群零监控栈 | `infra-community/helm-charts/openeuler-ci-cn4-cluster/`（`jenkins.yaml` + `project.yaml`） |
| B-4：31 条告警全无线 B 指向 | 同线 A 的 `prometheus-rules.yaml` |
| B-5：无门禁大盘 | `helm-chart-value-osw/common/grafana/prod/values-dashboards.yaml` |
| B-6：无线 B 侧故障归因资料 | `general/昇腾失败原因分析/`（全部针对 GitHub Actions + NPU） |
| B-7：缺结果性判断 | [backlog#1938](https://github.com/opensourceways/backlog/issues/1938) 最后一条评论 |
| webhook 链路、机器人不读 Jenkins 结果 | `community-robots/` 全仓 grep `jenkins` 零命中；`robot-universal-hook-delivery` → Kafka `metadata_webhook_gitcode` → `robot-hook-dispatcher` → `robot-universal-access` |
| CodeArts 迁移方向 | `community-robots/robot-universal-quality-gate-trigger/`（`config.go` 指向 CodeArts 流水线；两个 handler 仍为 `// TODO`） |
| 净室白名单 resolver 无监控 | `jenkins-log-scanner` 的 `vpc_ip_whitelist_auto_resolver`（ns `infra-security` CronJob） |
| 线 B 调研全文 | [backlog#2080 评论](https://github.com/opensourceways/backlog/issues/2080#issuecomment-6081591152) |
