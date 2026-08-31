# 资源中心 Qdrant 专项总进度

> 专项状态：`IN_PROGRESS`
> 当前阶段：P0 开发准备与决策冻结
> 当前里程碑：M0 决策与基线就绪
> 最后更新：2026-08-31
> 维护入口：[目录说明](README.md)

## 1. 总体摘要

当前已完成目标架构阅读、现状代码核对、相似实现核对和阶段拆分，并交付了 P0 静态基线检查点；尚未进入功能编码。P0 的 10 项关键决策均为开放状态，且本机缺少 Docker CLI，因此 P1 不应开始写生产 schema 或绑定具体 embedding/Qdrant 契约。

| 指标 | 当前值 |
|---|---:|
| 总任务 | 88 |
| 已完成任务 | 1 |
| 总体进度 | 1.1% |
| 开放决策 | 10 |
| 开放高/严重风险 | 5 |
| 当前阻断 | P0 决策尚未冻结；本机 Docker CLI 不可用，无法执行 Compose/Qdrant smoke |

进度按已完成任务数计算，只用于反映执行量，不代替阶段门禁。阶段未满足退出条件时，即使任务勾选率为 100%，也不能标记 `DONE`。

## 2. 阶段状态

| 阶段 | 状态 | 完成任务 | 依赖 | 里程碑 | 计划结果 |
|---|---|---:|---|---|---|
| [P0 开发准备与决策冻结](00-development-readiness.md) | `IN_PROGRESS` | 1/14 | 无 | M0 | 决策、SLO、基线和风险边界冻结 |
| [P1 数据与契约基础](01-data-and-contract-foundation.md) | `TODO` | 0/12 | P0 | M1 | schema、port、配置和开发 Qdrant 可用 |
| [P2 入库与向量索引](02-ingestion-and-vector-indexing.md) | `TODO` | 0/14 | P1 | M2 | 文档可异步、幂等地形成可检索向量 |
| [P3 检索与 RAG 集成](03-retrieval-and-rag-integration.md) | `TODO` | 0/13 | P2 | M3 | 混合检索和 Session RAG MVP 可用 |
| [P4 生产就绪](04-production-readiness.md) | `TODO` | 0/12 | P3 | M4 | 安全、观测、恢复和生产拓扑通过验收 |
| [P5 性能与质量](05-performance-and-quality.md) | `TODO` | 0/11 | P4 | M5 | 性能和检索质量达到已批准 SLO |
| [P6 多租户、高可用与智能增强](06-multitenancy-ha-and-intelligence.md) | `TODO` | 0/12 | P5 | M6 | 组合权限、隔离、容灾和高级能力可用 |

## 3. 里程碑门禁

| 里程碑 | 状态 | 通过条件 |
|---|---|---|
| M0 决策与基线就绪 | `IN_PROGRESS` | D-001 至 D-010 全部 `DECIDED`，代表性语料、质量/性能基线和威胁模型可复用 |
| M1 基础契约就绪 | `TODO` | forward migration、application ports、配置校验、Qdrant adapter 骨架和开发健康检查通过 |
| M2 入库索引闭环 | `TODO` | 上传到可检索向量全链路通过，崩溃重试、幂等、删除和对账行为可证明 |
| M3 MVP 可用 | `TODO` | 混合检索、PostgreSQL 最终鉴权、引用、Session 集成和 FTS 降级通过 |
| M4 生产就绪 | `TODO` | 生产拓扑、安全审计、告警、备份恢复、重建和故障演练通过 |
| M5 性能质量达标 | `TODO` | 代表性负载下 P95/P99/QPS 和离线检索指标达到批准阈值 |
| M6 高级能力就绪 | `TODO` | 多租户组合权限、隔离、RPO/RTO、模型切换和高级检索通过 |

## 4. 决策登记

状态只使用 `OPEN`、`DECIDED`、`DEFERRED`。`DEFERRED` 必须写明不影响当前阶段的理由和重新决策时间。

| ID | 决策 | 状态 | 负责人 | 截止 | 结论或记录 |
|---|---|---|---|---|---|
| D-001 | MVP 支持的 MIME、单文件大小、页数和批量上限 | `OPEN` | 待定 | P0 |  |
| D-002 | embedding provider、model、revision、dim、metric 与合规边界 | `OPEN` | 待定 | P0 |  |
| D-003 | collection、payload index、alias、shard key 命名、共享粒度与 placement 规则 | `OPEN` | 待定 | P0 |  |
| D-004 | 质量 SLO、评测集、Recall/MRR/nDCG 与引用正确率阈值 | `OPEN` | 待定 | P0 |  |
| D-005 | 性能 SLO、容量区间、并发、P95/P99 与成本预算 | `OPEN` | 待定 | P0 |  |
| D-006 | 强/最终一致性边界、版本保留、软删除和终态保留周期 | `OPEN` | 待定 | P0 |  |
| D-007 | ACL 优先级、deny 语义、默认租户/知识库与最终鉴权规则 | `OPEN` | 待定 | P0 |  |
| D-008 | Qdrant、embedding、rerank 故障时的降级模式与用户契约 | `OPEN` | 待定 | P0 |  |
| D-009 | PostgreSQL、对象存储和 Qdrant 的 RPO/RTO、备份及跨区要求 | `OPEN` | 待定 | P0 |  |
| D-010 | 单节点到 Qdrant cluster/Cloud 的切换阈值和生产拓扑 | `OPEN` | 待定 | P0 |  |

## 5. 风险登记

| ID | 风险 | 等级 | 状态 | 缓解和验证 |
|---|---|---|---|---|
| R-001 | PostgreSQL 与 Qdrant 状态漂移，导致旧版本向量被检索 | 高 | `OPEN` | transactional outbox、可靠 job、确定性 upsert、generation 切换和 reconcile |
| R-002 | Qdrant payload 过滤或缓存错误导致越权检索 | 严重 | `OPEN` | PostgreSQL 粗筛加最终鉴权；无最终授权结果不得进入上下文 |
| R-003 | embedding revision、维度、metric 或 payload schema 漂移污染同一 collection | 高 | `OPEN` | 模型契约、collection 分代、payload index 校验、蓝绿构建和原子切换 |
| R-004 | 解析器、embedding 或 Qdrant 长时间失败造成任务堆积 | 高 | `OPEN` | lease、heartbeat、有限重试、dead、告警、背压和 FTS 降级 |
| R-005 | 当前“创建即 PUBLISHED”与异步发布语义冲突 | 高 | `OPEN` | P0 冻结兼容策略，P1/P2 明确状态机、迁移和旧客户端行为 |
| R-006 | 当前租户模型不完整，却被误宣称为完整多租户隔离 | 严重 | `OPEN` | MVP 使用显式默认租户/知识库；P6 验收前不承诺完整多租户 |
| R-007 | 代表性数据不足导致索引和参数结论失真 | 中 | `OPEN` | P0 固定语料和查询集，P5 只基于目标规模实测作结论 |
| R-008 | 日志、错误或任务 payload 泄露原文、凭据或敏感 ACL | 高 | `OPEN` | 最小 payload、统一脱敏、错误码分层、日志采样和安全扫描 |

## 6. 跨阶段质量门禁

| 门禁 | 状态 | 证据要求 |
|---|---|---|
| PostgreSQL 是业务与权限唯一真相 | `TODO` | 所有发布、删除、版本和最终授权测试均以 PostgreSQL 结果为准 |
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

不在此表粘贴密钥、Token、密码、真实 DSN 或受保护业务数据。长日志保存到受控构建系统，表中只保留摘要和链接。

## 8. 近期行动

1. 指定 P0 技术负责人、产品负责人和安全/运维评审人，并逐项确认 D-001～D-010；若暂缓，补充 `DEFERRED` 理由和复核日期。
2. 提供带 Docker CLI 的验证环境或可访问的隔离 Qdrant 测试实例，完成 Compose、健康检查和可清理 smoke。
3. 固定代表性文档语料、查询集和权限矩阵，记录当前 PostgreSQL 搜索与 Session 基线。
4. P0 退出评审通过后再为 P1 创建开发分支和第一条 forward migration。

## 9. 更新记录

| 日期 | 变更 |
|---|---|
| 2026-08-30 | 根据目标架构和当前代码建立 P0-P6 专项阶段、决策、风险、门禁与证据跟踪。 |
| 2026-08-31 | 将资源中心向量数据库开发文档统一为 Qdrant，更新路径、术语、部署/索引/快照模型和交叉链接。 |
| 2026-08-31 | 完成 P0-01 静态基线检查点，补充阶段计划、验收命令、待确认决策矩阵和 Docker 环境阻断；P0 仍未通过门禁。 |
