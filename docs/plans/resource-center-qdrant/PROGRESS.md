# 资源中心 Qdrant 专项总进度

> 专项状态：`IN_PROGRESS`
> 当前阶段：P0-P2 已完成，P3 的 13 项开发已完成；真实质量与容量已验收，M3 待 5 次真实无答案问答验收
> 当前里程碑：M3 MVP 可用 `IN_PROGRESS`（未通过）
> 最后更新：2026-09-06
> 维护入口：[目录说明](README.md)
> 模型配置：管理员已激活 `voyage-4-large` 的系统版本 `auto-v2-e5ec9a9f2abaa010`（1024 维、Cosine、`send_dimensions=false`、32/30/3）；运行时只读取唯一 active 的不可变版本，代码、环境变量和普通请求方不得替代管理员选择。
> 执行范围：项目负责人已解除 P2-A 暂停门，授权在测试环境完成 P2-B、真实模型质量和容量评测。当前证据见[本轮验收](TEST-ACCEPTANCE-2026-09-06.md)，历史日志不代替当前结论。

## 1. 总体摘要

P0、P1/M1、P2/M2 已完成。PDF/DOCX/TXT/MD 异步入库、幂等任务、租约、发布/撤回/删除、重建/对账和保留清理均已实现并通过测试。60 篇原创讲义使用管理员真实模型生成向量；最终 100 条冻结查询的 Recall@5、MRR@5、nDCG@5 均为 1.0，500 次引用身份及原文校验通过。修复 PostgreSQL 扫描问题后，同版本 Linux Qdrant 的 10 万条/5 并发本地与完整链路 P95 为 427/622 ms，达到冻结门槛；百万档完成趋势评测，存在 4%-5% 降级。M3 唯一未完成验收是 5 次真实 Tutor 无答案问答，自动审批要求明确外部目的地调用授权。

| 指标 | 当前值 |
|---|---:|
| 总任务 | 93 |
| 已完成任务 | 58 |
| 总体进度 | 62.4% |
| 待后续决策 | 2（D-009/D-010，P4 生产范围） |
| 当前范围开放高/严重风险 | 0（P0-P3 已实施缓解；真实无答案问答仍待验收） |
| 当前执行门 | P2-B 14/14 已完成；质量和固定容量门槛通过，M3 待真实无答案问答 |

进度按已完成任务数计算，只用于反映执行量，不代替阶段门禁。阶段未满足退出条件时，即使任务勾选率为 100%，也不能标记 `DONE`。

## 2. 阶段状态

| 阶段 | 状态 | 完成任务 | 依赖 | 里程碑 | 计划结果 |
|---|---|---:|---|---|---|
| [P0 开发准备与决策冻结](00-development-readiness.md) | `DONE` | 14/14 | 无 | M0 | 决策、SLO、基线和风险边界冻结 |
| [P1 数据与契约基础](01-data-and-contract-foundation.md) | `DONE` | 12/12 | P0 | M1 | schema、port、配置和开发 Qdrant 已通过实机验证 |
| [P2 管理员模型配置、入库与向量索引](02-ingestion-and-vector-indexing.md) | `DONE` | 19/19 | P1 | M2-A/M2-B | 管理员模型、四类文档异步入库、租约重试、发布/清理、重建对账通过 |
| [P3 检索与 RAG 集成](03-retrieval-and-rag-integration.md) | `IN_PROGRESS` | 13/13 | P2 已完成；固定质量/容量门槛通过 | M3 | 真实质量、引用、容量通过；待真实无答案问答 |
| [P4 生产就绪](04-production-readiness.md) | `TODO` | 0/12 | P3 | M4 | 安全、观测、恢复和生产拓扑通过验收 |
| [P5 性能与质量](05-performance-and-quality.md) | `TODO` | 0/11 | P4 | M5 | 性能和检索质量达到已批准 SLO |
| [P6 多租户、高可用与智能增强](06-multitenancy-ha-and-intelligence.md) | `TODO` | 0/12 | P5 | M6 | 组合权限、隔离、容灾和高级能力可用 |

## 3. 里程碑门禁

| 里程碑 | 状态 | 通过条件 |
|---|---|---|
| M0 决策与基线就绪 | `DONE` | D-001 至 D-008 已决定；D-009/D-010 明确暂缓至 P4，测试环境语料、容量门槛与威胁边界已冻结 |
| M1 基础契约就绪 | `DONE` | forward migration、application ports、配置校验、Qdrant adapter、Compose/live health/schema、鉴权与完整读写清理 smoke 均通过 |
| M2-A 管理员模型配置就绪 | `DONE` | 已真实探测并原子激活唯一 active 不可变版本；运行时可脱敏解析配置。系统版本 `auto-v2-e5ec9a9f2abaa010` 复测为 1024 维；当时暂停门已于 2026-09-06 明确解除 |
| M2-B 入库索引闭环 | `DONE` | 暂停门已明确解除；四类文档完整链路、故障注入、幂等、清理和真实对账/重建通过 |
| M3 MVP 可用 | `IN_PROGRESS` | P2-B、真实质量、引用及固定容量门槛通过；待 5 次真实 Tutor 无答案问答 |
| M4 生产就绪 | `TODO` | 生产拓扑、安全审计、告警、备份恢复、重建和故障演练通过 |
| M5 性能质量达标 | `TODO` | 代表性负载下 P95/P99/QPS 和离线检索指标达到批准阈值 |
| M6 高级能力就绪 | `TODO` | 多租户组合权限、隔离、RPO/RTO、模型切换和高级检索通过 |

## 4. 决策登记

状态只使用 `OPEN`、`DECIDED`、`DEFERRED`。`DEFERRED` 必须写明不影响当前阶段的理由和重新决策时间。

| ID | 决策 | 状态 | 负责人 | 截止 | 结论或记录 |
|---|---|---|---|---|---|
| D-001 | MVP 支持的 MIME、单文件大小、页数和批量上限 | `DECIDED` | Codex | 2026-09-01 | PDF/DOCX/TXT/MD；50 MiB、200 页、200 万字符、单批 10 个、解析预算 120 秒；CLI 单任务含模型和索引总预算 10 分钟，禁压缩包/扫描 PDF |
| D-002 | embedding 费用与数据合规边界 | `DECIDED` | 项目负责人授权 / Codex 执行 | 2026-09-06 | 管理员 active `voyage-4-large` / `auto-v2-e5ec9a9f2abaa010` / 1024 / Cosine；仅原创数学数据，最多 200 万 UTF-8 文档字节和 1000 次查询，测试环境执行 |
| D-003 | collection、payload index、alias、shard key 命名、共享粒度与 placement 规则 | `DECIDED` | Codex | 2026-09-06 | 按知识库与 generation 分 collection，固定模型契约及 payload index；旧代保留直至新代原子切换，无 alias/custom shard |
| D-004 | 质量 SLO、评测集、Recall/MRR/nDCG 与引用正确率阈值 | `DECIDED` | Codex 独立标注后冻结 | 2026-09-06 | 60 篇原创语料、100 正例、10 负例；Recall@5 >= 0.90、MRR/nDCG >= 0.85，引用/权限正确率 100%，无关候选不得冒充回答证据 |
| D-005 | 性能 SLO、容量区间、并发、P95/P99 与成本预算 | `DECIDED` | Codex 测试环境基线 | 2026-09-06 | 10 万向量、5 并发，本地检索 P95 <= 1 秒，含真实 embedding <= 3 秒；100 万副本档只记录扩展趋势，非生产 SLO |
| D-006 | 强/最终一致性边界、版本保留、软删除和终态保留周期 | `DECIDED` | Codex | 2026-09-06 | PG 强一致、Qdrant 最终一致；旧向量 7 天、终态 job/outbox 30 天，保留最后状态摘要；未引用 staging 24 小时后有租约地回收 |
| D-007 | ACL 优先级、deny 语义、默认租户/知识库与最终鉴权规则 | `DECIDED` | Codex/安全基线 | 2026-09-01 | `default` tenant/kb，owner + ACL，deny 优先，PG 最终复核 |
| D-008 | Qdrant、embedding、rerank 故障时的降级模式与用户契约 | `DECIDED` | Codex | 2026-09-01 | FTS-only/degraded、融合回退、鉴权 fail closed |
| D-009 | PostgreSQL、对象存储和 Qdrant 的 RPO/RTO、备份及跨区要求 | `DEFERRED` | 运维/安全评审 | 2026-09-15 | P4 前不宣称生产灾备；先保证可重建和可清理 |
| D-010 | 单节点到 Qdrant cluster/Cloud 的切换阈值和生产拓扑 | `DEFERRED` | 运维/架构评审 | 2026-09-15 | 开发/集成单节点；>1M vectors、峰值 QPS 20 或 HA 要求时评估 cluster/Cloud |

## 5. 风险登记

`MITIGATED` 表示当前 MVP 范围已实现控制并验证，不表示未来生产风险消失。P4 的生产拓扑、完整备份恢复、告警接入和 P6 的完整多租户验收单独跟踪，不计为 P0-P3 未完成开发；M3 质量与容量门禁仍按最终证据判定。

| ID | 风险 | 等级 | 状态 | 缓解和验证 |
|---|---|---|---|---|
| R-001 | PostgreSQL 与 Qdrant 状态漂移，导致旧版本向量被检索 | 高 | `MITIGATED` | 真实缺 1/错 1/多 1 对账修复后差异为 0；幂等、发布屏障、原子切代和撤销后旧引用拒绝已验证 |
| R-002 | Qdrant payload 过滤或缓存错误导致越权检索 | 严重 | `MITIGATED` | 两次 PG 授权、manifest 身份核对、deny/停用/撤权竞态与 Session 历史隔离已验证；当前无检索缓存，引用 no-store 且每次重新鉴权 |
| R-003 | embedding revision、维度、metric 或 payload schema 漂移污染同一 collection | 高 | `MITIGATED` | 管理员不可变 active 契约、来源漂移失败关闭、每代独立 collection、schema/payload 校验及真实蓝绿重建通过 |
| R-004 | 解析器、embedding 或 Qdrant 长时间失败造成任务堆积 | 高 | `MITIGATED` | 解析限额、lease/heartbeat、有限重试/dead、1000 槽背压、FTS 降级和固定队列指标通过；生产告警系统接入与长期负载归 P4/P5 |
| R-005 | 旧“创建即 PUBLISHED”与文档异步发布语义冲突 | 高 | `MITIGATED` | 新文档独立 202 入库并在索引验证后发布；旧资源接口保留兼容，已入库正文不可被旧编辑覆盖，旧删除进入撤销与 purge 流程 |
| R-006 | 默认租户模型被误宣称为完整多租户隔离 | 严重 | `MITIGATED` | 当前限定 default tenant/kb，owner/ACL 与错误租户负向验证通过；正式租户领域、RLS、placement 和完整多租户验收明确属于 P6 |
| R-007 | 代表性数据不足导致索引和参数结论失真 | 中 | `OPEN` | 60 篇原创语料、100 条冻结查询及固定 10 万容量已验证；真实无答案输出待验收。百万副本只记趋势，不外推教学效果或生产 SLO；代表性数据扩充归 P5 |
| R-008 | 日志、错误或任务 payload 泄露原文、凭据或敏感 ACL | 高 | `MITIGATED` | 入库/存储/模型边界返回固定安全码，Qdrant 仅存最小身份/hash，状态与指标不返回来源或凭据；外部失败 Mock 与脱敏检查通过，生产审计接入归 P4 |

## 6. 跨阶段质量门禁

| 门禁 | 状态 | 证据要求 |
|---|---|---|
| PostgreSQL 是业务与权限唯一真相 | `DONE` | 真实发布、撤销、版本切换、manifest 核验与最终授权通过，向量残留不恢复读取权限 |
| 管理员模型配置 | `DONE` | 模型已由管理员在管理端测试并激活；运行时无 active 版本时禁用向量链路，不回退到代码或环境变量默认值 |
| Qdrant client 隔离 | `DONE` | application/Session/LLM 仅依赖 ports；Qdrant adapter 只由 cmd 装配，边界检查与全仓构建通过 |
| 幂等与崩溃恢复 | `DONE` | 真实 PG 重放、并发 claim、失效租约、有限重试与事务回滚验证通过，确定性 upsert/reconcile 恢复后无差异 |
| 当前 MVP 无权限泄露 | `DONE` | 角色、所有者、默认知识库、deny、下线/删除、引用 no-store 和授权竞态负向验证通过；未启用检索缓存，未来缓存与完整多租户另验 |
| 可降级 | `DONE` | Qdrant、embedding、rerank 单独故障符合 D-008，FTS 保留、重排回退、最终鉴权 fail closed，错误不返回 provider 原文 |
| MVP 代际重建与撤销 | `DONE` | 真实 60 文档重建在切换前保留旧代服务，切换后新引用有效且旧引用拒绝；撤销与向量清理、对账均通过 |
| 生产备份恢复与 RPO/RTO | `TODO`（P4） | PostgreSQL、对象存储和 Qdrant 完整恢复、生产容器与容灾演练属于后续生产验收，不作为 P3 未完成开发 |
| MVP 检索质量与目标容量 | `DONE` | 最终真实正例质量及 500 次引用核验通过；10 万条/5 并发本地及完整链路 P95 为 427/622 ms，均达到冻结门槛 |
| MVP 真实无答案问答 | `IN_PROGRESS` | 5 次 Tutor 调用待外部目的地授权；需核对真实回答不把无关候选作为论据，当前不标记 M3 DONE |

## 7. 验证证据日志

| 日期 | 阶段/任务 | 环境 | 命令或演练 | 结果 | 证据位置 |
|---|---|---|---|---|---|
| 2026-08-30 | 计划建立 | 本地工作区 | CodeGraph/FastCtx 现状核对 | 已确认当前实现边界，尚未运行功能验证 | 本目录及目标架构 |
| 2026-08-30 | 计划文档与仓库基线验证 | 本地工作区 | 任务/链接/敏感信息检查；后端 test/vet/build；前端 test/lint/build | 文档检查与构建通过；前端默认测试因无测试文件退出 1，使用 `--passWithNoTests` 复核通过；未执行 Qdrant 功能验证 | 本次任务执行记录 |
| 2026-08-31 | 向量数据库选型迁移 | 本地工作区 | Qdrant 官方术语核对、文档链接和残留引用检查 | 技术方案与 P0-P6 计划已统一为 Qdrant 语义；尚未执行 Qdrant 功能验证 | 本次任务执行记录 |
| 2026-08-31 | P0-01 基线检查点 | 本地工作区 | `go test ./... -count=1`；`go vet ./...`；`go build ./...`；`npm run lint`；`npm run build`；`git diff --check` | Go/前端基线与文档差异检查通过；Docker CLI 缺失，Compose/Qdrant smoke 未执行 | `00-development-readiness.md` 第 5.1 节 |
| 2026-09-01 | P0 决策冻结 | 本地工作区 | 决策矩阵、阶段门禁和敏感信息检查；`docker --version`；`docker compose version` | 5 项决定、5 项按日期暂缓；P0 允许进入 P1；Docker CLI 未安装，live smoke 待外部环境 | `00-development-readiness.md` 第 4.1、7 节 |
| 2026-09-01 | P1 契约实现 | 本地工作区 | 全量 Go test/vet/build、前端 lint/build、隔离 PostgreSQL 迁移首次/重复执行、临时 `httptest` adapter smoke（85.8% statements）、`docker compose --profile vector config --quiet`、`git diff --check` | migration、ports、配置、adapter、Compose profile 和装配完成；兼容旧 `contents` 写入的默认租户验证通过；Compose 配置解析通过；Docker CLI/Compose 可调用但 Docker Desktop Linux 引擎因 WSL 运行时错误未启动，live smoke 待修复运行时或外部环境 | `01-data-and-contract-foundation.md` 第 8 节 |
| 2026-09-01 | P1 live smoke 环境诊断 | 本地工作区 | `docker desktop status`；Docker Desktop 诊断日志；`wsl.exe -l -v --all` | 状态持续为 `starting`；日志确认 `docker-desktop` 发行版缺失，并在导入 `C:\\Program Files\\WSL\\system.vhd` 时返回 `Wsl/Service/RegisterDistro/CreateVm/MountDisk/HCS/ERROR_NOT_FOUND`。未执行 WSL 安装、注销或重置等宿主机变更 | 本次任务执行记录 |
| 2026-09-01 | P1 Qdrant 实机验收 | Docker Desktop/WSL2、`qdrant/qdrant:v1.14.1` | Compose config/up/health；临时 Go live smoke；后端全量 test/vet/build；前端 lint/build | 容器达到 `healthy`；无鉴权和随机临时 API key 模式均通过 collection/schema、5 个 payload index、重复 upsert、过滤检索、schema mismatch、按 ID/过滤删除及清理；修复无 `curl` healthcheck、空 key 鉴权语义和 payload index REST 契约；临时源码/collection 已删除且 key 未持久化，M1 通过 | `01-data-and-contract-foundation.md` 第 8 节 |
| 2026-09-01 | P2-A 文档执行门统一 | 本地工作区 | 9 文件口径/任务计数/旧表述/敏感信息检查；`git diff --check`；后端 `go test ./... -count=1`、`go build ./...`；前端 `npm test -- --run --passWithNoTests`、`npm run lint`、`npm run build` | 9 份阶段文档均明确管理员模型配置与 P2-A 后强制暂停；任务总数校准为 93，旧冲突表述和敏感信息无残留；测试、lint、构建与差异检查通过 | 本次任务执行记录 |
| 2026-09-04 | P2-A/M2-A 真实激活与兼容性复测 | 本地工作区、管理员配置、真实 embedding provider | 管理端真实 probe/activation；完整 active 契约复测；多 Key、有限重试与不可变 revision 临时 Mock；后端 test/vet/build、前端 test/lint/build、桌面与移动端 UI 验证 | 已激活 `voyage-4-large` 的系统版本 `auto-v2-e5ec9a9f2abaa010`（1024 维、Cosine、`send_dimensions=false`、32/30/3）。通用探针携带可选 `encoding_format` 时上游 HTTP 400；省略后完整契约与模型优先流程均复测成功，最终浏览器探测约 1.00 秒。多 Key 渠道逐 Key 验证且共享最多 20 次额外重试；自动 revision 绑定来源与完整契约。P2-A/M2-A 完成并进入强制暂停；D-002 费用与数据合规仍待确认，Qdrant 当前不可用，P2-B/P3 未启动 | `02-ingestion-and-vector-indexing.md` 第 8 节及本次任务执行记录 |
| 2026-09-05 | P3 只读检索切片 | 本地 Mock、隔离 PostgreSQL 18 | 新增函数覆盖验证；18 个 migration；31 项 PG 场景、退役降级修正后 14 项场景、4 项 HTTP→应用→PG 验证；`go test -race ./... -count=1`、`go vet ./...`、`go build ./...`；前端 test/lint/build；差异与敏感信息检查 | 均通过；新增受测函数语句覆盖 100%。修复模型退役导致 FTS 不可用的问题；临时测试、fixture、覆盖产物与隔离 PG 实例已清理。P3 为 5/13，P2-B/M3 尚未完成，未调用外部模型 | `03-retrieval-and-rag-integration.md` 第 8 节 |
| 2026-09-06 | P3 全部 13 项开发 | 隔离 PostgreSQL 18、Qdrant 1.14.1 Windows 原生、模型 Mock、真实浏览器 | 19 个迁移；23 ACL 组合、邻接/引用/manifest/1001资源边界、PG+Qdrant+HTTP；Session 持久化；一万chunk/五并发；全仓 race/vet/build；前端测试/lint/build与320/390/1440截图 | 通过；新核心函数覆盖至少83.3%，Search/引用/重排流程/metrics 100%。FTS P95 409ms、Mock hybrid 334ms；合成精确术语 Recall/MRR/nDCG为1、引用身份/hash正确率100%，不代表真实语义质量。P3 13/13，总44/93；M3 保留未通过 | `03-retrieval-and-rag-integration.md` 第 8.2 节 |
| 2026-09-06 | P3 提交前开发交付验收 | 本地工作区、独立代码复核 | 后端 vet/build、前端 lint/build、Go 格式、差异/敏感信息/临时文件检查；核对此前集成与浏览器证据 | 开发交付验收通过，未发现阻止提交的问题；此前临时测试已清理，本次未将空测试当功能复测。M3 仍待 P2-B 与代表性质量/性能验收 | `03-retrieval-and-rag-integration.md` 第 8.3 节 |
| 2026-09-06 | P2-B 与真实质量、一致性 | 隔离 PG 18、Qdrant 1.14.1、原创语料与管理员模型 | 21 个迁移；四格式真实上传/发布/引用/删除；幂等、租约、故障回滚、对账、重建、终态及 staging 清理；60 文档与 100 查询 | P2-B 14/14、P2 19/19 完成；Recall/MRR/nDCG@5 均为 1.0，500 次引用通过；缺/错/多故障修复后差异 0，旧代切换与保留正确。容量及无答案输出独立收口 | `TEST-ACCEPTANCE-2026-09-06.md` |
| 2026-09-06 | 最终工程检查与前端清理 | 本地工作区、临时测试与 Mock | 全仓 `go test -race ./...`、`go vet ./...`、`go build ./...`；前端定向 Vitest、`tsc -b`、lint 与已有生产 build | 后端全仓检查通过；前端 23/23，TypeScript/lint 通过，清理后 lint 0 错误/0 告警；前端本次测试、QA 脚本和 fixture 已删。其余验收产物清理及 Git 检查在运行验收结束后收尾 | `TEST-ACCEPTANCE-2026-09-06.md` 及本次验收记录 |
| 2026-09-06 | 最终质量、容量与清理回归 | 隔离 PG 18、Linux Qdrant 1.14.1、真实 embedding | 100 条质量查询；10 万/百万向量各 1/5/10 并发；有界响应 drain 的 Mock/race；副本恢复、暂存登记与旧代到期演练 | 三个质量指标均为 1，引用 500/500；10 万/5 并发 P95 427/622 ms 通过；百万档完成且记录 4%-5% 降级；999940 副本清除，旧代 60 点到期删除，当前 60 点且对账差异为 0 | `TEST-ACCEPTANCE-2026-09-06.md` |
| 2026-09-06 | 本轮交付清理 | 本地工作区 | 临时文件与服务清理；生产代码 `go build ./...`；`git diff --check`；敏感信息检查 | 通过；临时测试、评测工具、语料、隔离数据库及向量数据均已清理，保留验收摘要与截图。真实 Tutor 无答案验收未执行，不计作通过 | `TEST-ACCEPTANCE-2026-09-06.md` |

不在此表粘贴密钥、Token、密码、真实 DSN 或受保护业务数据。长日志保存到受控构建系统，表中只保留摘要和链接。

## 8. 近期行动

1. 获得 5 次真实 Tutor 外部目的地调用授权后，逐条核验无答案回答不把无关候选作为证据，记录结论；未通过前 M3 保持 `IN_PROGRESS`。
2. 真实无答案验收通过后同步 M3/P3 状态；本轮其他开发、质量、目标容量、工程验证与临时产物清理均已完成。
3. P4 独立验收生产容器、备份恢复、RPO/RTO 与正式告警接入；P5 处理百万档资源预算、中文词法索引与更大规模调优。本轮没有 Docker 镜像构建证据。

## 9. 更新记录

| 日期 | 变更 |
|---|---|
| 2026-09-06 | 固定 10 万目标容量通过，百万档完成趋势评测并记录降级；最终质量、引用、副本清除、暂存登记和旧代到期验证通过。M3 仅待 5 次真实 Tutor 无答案问答的调用授权与答案验收。 |
| 2026-09-06 | 记录 P2-B 14/14 和真实质量、一致性证据，总任务更新为 58/93；D-002/D-004/D-005 已在测试范围决定。清除总页过时的 P2-B 待实施和再次授权事项，已验证 MVP 基础门禁标 DONE、风险标 MITIGATED；P4 生产恢复和 P6 完整多租户单独保留，M3 仍等待固定容量与无答案最终验收。 |
| 2026-09-06 | 按“推进至 P3 全部完成”交付余下8项开发，P3由5/13更新为13/13，总任务44/93。真实隔离PG/Qdrant、会话、浏览器和全仓检查通过；新增0019迁移、引用再次鉴权、动态预算及指标。复核补齐邻接独立超时及0页引用兼容。M3仍等待P2-B和代表性质量/性能验收。 |
| 2026-09-05 | 根据继续推进第三阶段指令先行交付 P3 只读检索切片：请求、FTS 粗授权/召回、RRF、最终权限复核与 HTTP 装配。P3 5/13，总任务 36/93；保留 P2-B、D-002 及完整 M3 验收依赖，未发起模型调用。 |
| 2026-08-30 | 根据目标架构和当前代码建立 P0-P6 专项阶段、决策、风险、门禁与证据跟踪。 |
| 2026-08-31 | 将资源中心向量数据库开发文档统一为 Qdrant，更新路径、术语、部署/索引/快照模型和交叉链接。 |
| 2026-08-31 | 完成 P0-01 静态基线检查点，补充阶段计划、验收命令、待确认决策矩阵和 Docker 环境阻断；P0 仍未通过门禁。 |
| 2026-09-01 | 冻结 P0 工程决策和暂缓项，P0 通过 P1 前置门，启动 P1；Docker CLI 缺失继续作为 live Qdrant smoke 阻断。 |
| 2026-09-01 | 完成 P1 基础契约实现和 Mock 验证；M1 保持 `IN_PROGRESS`，Docker Desktop Linux 引擎的 WSL 挂载错误已定位，等待运行时修复或外部 Docker/Qdrant live smoke 后再决定是否进入 P2。 |
| 2026-09-01 | Docker Desktop/WSL2 恢复后完成 P1 Qdrant 实机验收，修复 Compose health/API key 与 payload index 契约问题；P1 标记 `DONE`、M1 通过。当时记录为等待 D-002，后由下一条 P2-A/P2-B 拆分决策取代。 |
| 2026-09-01 | 将 P2 拆分为 P2-A 管理员模型配置与 P2-B 入库索引；明确实际 embedding 模型只能由管理员在管理端测试并激活，并设置 P2-A 完成后的强制暂停门。 |
| 2026-09-04 | 完成 P2-A/M2-A：管理员真实激活 `voyage-4-large` 的系统版本 `auto-v2-e5ec9a9f2abaa010`（1024 维、Cosine、`send_dimensions=false`、32/30/3）；发现通用探针携带可选 `encoding_format` 会收到上游 HTTP 400，省略后完整契约和模型优先流程复测成功。UI 收敛为仅模型必选、测试自动识别维度、revision 内部自动生成和高级参数折叠；多 Key 渠道逐 Key 验证，瞬时错误有限退避且整次验证最多追加 20 次重试，自动 revision 与不可变 identity 均绑定来源和完整契约。P2 进入强制暂停；D-002 费用与数据合规仍未确认，Qdrant 当前不可用，P2-B/P3 未启动。 |
