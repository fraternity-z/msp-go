# 资源中心 Qdrant 专项总进度

> 专项状态：`IN_PROGRESS`
> 当前阶段：P2 强制暂停（P2-A 已完成）
> 当前里程碑：M2-A 管理员模型配置就绪 `DONE`
> 最后更新：2026-09-05
> 维护入口：[目录说明](README.md)
> 模型配置：管理员已激活 `voyage-4-large` 的系统版本 `auto-v2-e5ec9a9f2abaa010`（1024 维、Cosine、`send_dimensions=false`、32/30/3）；运行时只读取唯一 active 的不可变版本，代码、环境变量和普通请求方不得替代管理员选择。
> 执行暂停：P2-A 已完成并强制暂停。D-002 的费用与数据合规仍未确认，Qdrant 当前不可用；等待 D-002 决策和项目负责人明确继续，不得自动进入 P2-B 或 P3-P6。

## 1. 总体摘要

P0 已完成决策冻结和基线记录。P1 的 schema、application ports、配置、Qdrant adapter、Compose profile 和 API 装配已实现，并通过 Mock、静态、迁移及真实 Qdrant smoke，M1 已通过。P2-A 的管理员列表、测试、原子激活、不可变历史、运行时解析和管理界面已完成真实验证；`resource_embedding` 已激活 `voyage-4-large` 的系统版本 `auto-v2-e5ec9a9f2abaa010`。通用探针携带可选 `encoding_format` 时上游 HTTP 400，省略后按完整 active 契约复测成功（1024 维），M2-A 通过。D-002 的费用与数据合规仍按日期暂缓，Qdrant 当前不可用；P2 已强制暂停，P2-B/P3 未启动，恢复还需要 D-002 决策和项目负责人明确指令。

| 指标 | 当前值 |
|---|---:|
| 总任务 | 93 |
| 已完成任务 | 31 |
| 总体进度 | 33.3% |
| 开放决策 | 5 |
| 开放高/严重风险 | 5 |
| 当前执行门 | P2-A/M2-A 已完成并暂停。P2-B 只有在 D-002 已更新、Qdrant 可用且项目负责人明确解除暂停后才能启动 |

进度按已完成任务数计算，只用于反映执行量，不代替阶段门禁。阶段未满足退出条件时，即使任务勾选率为 100%，也不能标记 `DONE`。

## 2. 阶段状态

| 阶段 | 状态 | 完成任务 | 依赖 | 里程碑 | 计划结果 |
|---|---|---:|---|---|---|
| [P0 开发准备与决策冻结](00-development-readiness.md) | `DONE` | 14/14 | 无 | M0 | 决策、SLO、基线和风险边界冻结 |
| [P1 数据与契约基础](01-data-and-contract-foundation.md) | `DONE` | 12/12 | P0 | M1 | schema、port、配置和开发 Qdrant 已通过实机验证 |
| [P2 管理员模型配置、入库与向量索引](02-ingestion-and-vector-indexing.md) | `IN_PROGRESS` | 5/19 | P1 | M2-A/M2-B | P2-A/M2-A 已完成并强制暂停；解除暂停后 P2-B 才实现异步、幂等向量入库 |
| [P3 检索与 RAG 集成](03-retrieval-and-rag-integration.md) | `TODO` | 0/13 | P2-B + 暂停门解除 | M3 | 混合检索和 Session RAG MVP 可用 |
| [P4 生产就绪](04-production-readiness.md) | `TODO` | 0/12 | P3 | M4 | 安全、观测、恢复和生产拓扑通过验收 |
| [P5 性能与质量](05-performance-and-quality.md) | `TODO` | 0/11 | P4 | M5 | 性能和检索质量达到已批准 SLO |
| [P6 多租户、高可用与智能增强](06-multitenancy-ha-and-intelligence.md) | `TODO` | 0/12 | P5 | M6 | 组合权限、隔离、容灾和高级能力可用 |

## 3. 里程碑门禁

| 里程碑 | 状态 | 通过条件 |
|---|---|---|
| M0 决策与基线就绪 | `DONE` | D-001、D-003、D-006、D-007、D-008 已决定；D-002、D-004、D-005、D-009、D-010 已记录合规暂缓和复核日期，代表性基线与威胁边界可供后续复用 |
| M1 基础契约就绪 | `DONE` | forward migration、application ports、配置校验、Qdrant adapter、Compose/live health/schema、鉴权与完整读写清理 smoke 均通过 |
| M2-A 管理员模型配置就绪 | `DONE` | 已真实探测并原子激活唯一 active 不可变版本；运行时可脱敏解析配置。当前系统版本 `auto-v2-e5ec9a9f2abaa010` 复测为 1024 维；完成后已暂停 |
| M2-B 入库索引闭环 | `TODO` | 暂停门已明确解除；上传到可检索向量全链路通过，崩溃重试、幂等、删除和对账行为可证明 |
| M3 MVP 可用 | `TODO` | 混合检索、PostgreSQL 最终鉴权、引用、Session 集成和 FTS 降级通过 |
| M4 生产就绪 | `TODO` | 生产拓扑、安全审计、告警、备份恢复、重建和故障演练通过 |
| M5 性能质量达标 | `TODO` | 代表性负载下 P95/P99/QPS 和离线检索指标达到批准阈值 |
| M6 高级能力就绪 | `TODO` | 多租户组合权限、隔离、RPO/RTO、模型切换和高级检索通过 |

## 4. 决策登记

状态只使用 `OPEN`、`DECIDED`、`DEFERRED`。`DEFERRED` 必须写明不影响当前阶段的理由和重新决策时间。

| ID | 决策 | 状态 | 负责人 | 截止 | 结论或记录 |
|---|---|---|---|---|---|
| D-001 | MVP 支持的 MIME、单文件大小、页数和批量上限 | `DECIDED` | Codex | 2026-09-01 | PDF/DOCX/TXT/MD；50 MiB、200 页、200 万字符、单批 10 个、120 秒；禁压缩包/扫描 PDF |
| D-002 | embedding 费用与数据合规边界 | `DEFERRED` | 管理员/安全评审 | 2026-09-08 | provider、model、revision、dimension 和 metric 已由管理员激活为 `voyage-4-large` / `auto-v2-e5ec9a9f2abaa010` / 1024 / Cosine，且不写死于代码或环境变量。费用与数据合规尚未确认，P2-B 不得进入 |
| D-003 | collection、payload index、alias、shard key 命名、共享粒度与 placement 规则 | `DECIDED` | Codex | 2026-09-01 | 共享 dense collection、COSINE、强制 payload index；MVP 无 alias/custom shard |
| D-004 | 质量 SLO、评测集、Recall/MRR/nDCG 与引用正确率阈值 | `DEFERRED` | 产品/教学评审 | 2026-09-08 | 补代表性语料和标注责任后冻结；此前不宣称质量达标 |
| D-005 | 性能 SLO、容量区间、并发、P95/P99 与成本预算 | `DEFERRED` | 运维/产品评审 | 2026-09-08 | 补目标负载和预算后冻结；开发小数据只做可执行性验证 |
| D-006 | 强/最终一致性边界、版本保留、软删除和终态保留周期 | `DECIDED` | Codex | 2026-09-01 | PG 强一致、Qdrant 最终一致；旧 generation 7 天、终态任务 30 天 |
| D-007 | ACL 优先级、deny 语义、默认租户/知识库与最终鉴权规则 | `DECIDED` | Codex/安全基线 | 2026-09-01 | `default` tenant/kb，owner + ACL，deny 优先，PG 最终复核 |
| D-008 | Qdrant、embedding、rerank 故障时的降级模式与用户契约 | `DECIDED` | Codex | 2026-09-01 | FTS-only/degraded、融合回退、鉴权 fail closed |
| D-009 | PostgreSQL、对象存储和 Qdrant 的 RPO/RTO、备份及跨区要求 | `DEFERRED` | 运维/安全评审 | 2026-09-15 | P4 前不宣称生产灾备；先保证可重建和可清理 |
| D-010 | 单节点到 Qdrant cluster/Cloud 的切换阈值和生产拓扑 | `DEFERRED` | 运维/架构评审 | 2026-09-15 | 开发/集成单节点；>1M vectors、峰值 QPS 20 或 HA 要求时评估 cluster/Cloud |

## 5. 风险登记

| ID | 风险 | 等级 | 状态 | 缓解和验证 |
|---|---|---|---|---|
| R-001 | PostgreSQL 与 Qdrant 状态漂移，导致旧版本向量被检索 | 高 | `OPEN` | transactional outbox、可靠 job、确定性 upsert、generation 切换和 reconcile |
| R-002 | Qdrant payload 过滤或缓存错误导致越权检索 | 严重 | `OPEN` | PostgreSQL 粗筛加最终鉴权；无最终授权结果不得进入上下文 |
| R-003 | embedding revision、维度、metric 或 payload schema 漂移污染同一 collection | 高 | `OPEN` | 管理员测试后原子激活不可变模型版本；运行时只读 active 版本，并配合 collection 分代、payload index 校验、蓝绿构建和原子切换 |
| R-004 | 解析器、embedding 或 Qdrant 长时间失败造成任务堆积 | 高 | `OPEN` | lease、heartbeat、有限重试、dead、告警、背压和 FTS 降级 |
| R-005 | 当前“创建即 PUBLISHED”与异步发布语义冲突 | 高 | `OPEN` | P0 冻结兼容策略，P1/P2 明确状态机、迁移和旧客户端行为 |
| R-006 | 当前租户模型不完整，却被误宣称为完整多租户隔离 | 严重 | `OPEN` | MVP 使用显式默认租户/知识库；P6 验收前不承诺完整多租户 |
| R-007 | 代表性数据不足导致索引和参数结论失真 | 中 | `OPEN` | P0 固定语料和查询集，P5 只基于目标规模实测作结论 |
| R-008 | 日志、错误或任务 payload 泄露原文、凭据或敏感 ACL | 高 | `OPEN` | 最小 payload、统一脱敏、错误码分层、日志采样和安全扫描 |

## 6. 跨阶段质量门禁

| 门禁 | 状态 | 证据要求 |
|---|---|---|
| PostgreSQL 是业务与权限唯一真相 | `TODO` | 所有发布、删除、版本和最终授权测试均以 PostgreSQL 结果为准 |
| 管理员模型配置 | `DONE` | 模型已由管理员在管理端测试并激活；运行时无 active 版本时禁用向量链路，不回退到代码或环境变量默认值 |
| Qdrant client 隔离 | `TODO` | client import 只存在于 `backend/internal/adapter/qdrant` 及其装配边界 |
| 幂等与崩溃恢复 | `TODO` | 同一版本重复投递不产生重复 chunk；关键故障点重启后可收敛 |
| 无权限泄露 | `TODO` | 角色、所有者、默认知识库、删除/下线和缓存命中路径均有负向验证 |
| 可降级 | `TODO` | Qdrant、embedding、rerank 单独故障时符合 D-008，且错误信息可定位但不泄密 |
| 可回滚与可重建 | `TODO` | 应用、schema、generation、对象和向量恢复步骤均有演练记录 |
| 质量与性能 | `TODO` | 固定评测集和固定负载下的基线、阈值、回归差异可追溯 |

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

不在此表粘贴密钥、Token、密码、真实 DSN 或受保护业务数据。长日志保存到受控构建系统，表中只保留摘要和链接。

## 8. 近期行动

1. 安全评审确认外部数据合规和费用，将 D-002 更新为 `DECIDED`；不在文档或日志记录凭据。
2. 排查并恢复 Qdrant 可用性；暂停期间不实现 P2-B、P3 或其他后续能力。
3. 只有 D-002 已决策、Qdrant 可用且项目负责人明确下达继续指令后，才解除暂停并启动 P2-B；启动时重新核对 Git 状态、阶段文档、至少 3 个类似实现和实际调用链。

## 9. 更新记录

| 日期 | 变更 |
|---|---|
| 2026-08-30 | 根据目标架构和当前代码建立 P0-P6 专项阶段、决策、风险、门禁与证据跟踪。 |
| 2026-08-31 | 将资源中心向量数据库开发文档统一为 Qdrant，更新路径、术语、部署/索引/快照模型和交叉链接。 |
| 2026-08-31 | 完成 P0-01 静态基线检查点，补充阶段计划、验收命令、待确认决策矩阵和 Docker 环境阻断；P0 仍未通过门禁。 |
| 2026-09-01 | 冻结 P0 工程决策和暂缓项，P0 通过 P1 前置门，启动 P1；Docker CLI 缺失继续作为 live Qdrant smoke 阻断。 |
| 2026-09-01 | 完成 P1 基础契约实现和 Mock 验证；M1 保持 `IN_PROGRESS`，Docker Desktop Linux 引擎的 WSL 挂载错误已定位，等待运行时修复或外部 Docker/Qdrant live smoke 后再决定是否进入 P2。 |
| 2026-09-01 | Docker Desktop/WSL2 恢复后完成 P1 Qdrant 实机验收，修复 Compose health/API key 与 payload index 契约问题；P1 标记 `DONE`、M1 通过。当时记录为等待 D-002，后由下一条 P2-A/P2-B 拆分决策取代。 |
| 2026-09-01 | 将 P2 拆分为 P2-A 管理员模型配置与 P2-B 入库索引；明确实际 embedding 模型只能由管理员在管理端测试并激活，并设置 P2-A 完成后的强制暂停门。 |
| 2026-09-04 | 完成 P2-A/M2-A：管理员真实激活 `voyage-4-large` 的系统版本 `auto-v2-e5ec9a9f2abaa010`（1024 维、Cosine、`send_dimensions=false`、32/30/3）；发现通用探针携带可选 `encoding_format` 会收到上游 HTTP 400，省略后完整契约和模型优先流程复测成功。UI 收敛为仅模型必选、测试自动识别维度、revision 内部自动生成和高级参数折叠；多 Key 渠道逐 Key 验证，瞬时错误有限退避且整次验证最多追加 20 次重试，自动 revision 与不可变 identity 均绑定来源和完整契约。P2 进入强制暂停；D-002 费用与数据合规仍未确认，Qdrant 当前不可用，P2-B/P3 未启动。 |
