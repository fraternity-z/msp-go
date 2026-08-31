# 资源中心 Qdrant 专项开发计划

> 状态：`TODO`
> 建立日期：2026-08-30
> 目标架构：[资源中心 PostgreSQL + Qdrant 双数据库方案](../../technical/resource-center-qdrant-architecture.md)
> 专项总跟踪：[PROGRESS.md](PROGRESS.md)

## 1. 文档定位

本目录把目标架构拆成可执行、可验收、可回退的开发阶段。目标架构文档负责说明最终设计，本目录负责回答“先做什么、做到什么算完成、证据记录在哪里”。

项目级未完成事项仍以 [项目待办](../../TODO.md) 为唯一总入口。本目录只跟踪资源中心 Qdrant 专项，不复制其他模块待办；专项里程碑变化时，应在项目待办保留一条摘要并链接到 `PROGRESS.md`。

当前代码事实以磁盘中的最新实现为准。目标架构与当前实现不一致时，不把目标设计误写成已上线能力。

## 2. 当前基线

截至 2026-08-30，已核对的实现基线如下：

| 范围 | 当前事实 | 计划影响 |
|---|---|---|
| 资源业务 | `backend/internal/application/resource` 通过 Repository port 承载列表、详情、创建、更新、软删除、收藏和统计 | 延续 application port 模式，不让业务层依赖 Qdrant client |
| PostgreSQL | `contents` 是内容根，附件位于 `content_assets`，收藏位于 `user_favorites` | 保持 PostgreSQL 为业务与权限唯一真相 |
| 发布语义 | 当前教师资源创建后直接写入 `PUBLISHED` | P0 必须决定兼容与迁移策略，目标链路改为异步处理后发布 |
| ACL | 已有 `content_acl`，当前粒度不足以表达目标租户、知识库、用户/部门/角色组合权限 | MVP 先定义默认租户和默认知识库，最终授权仍由 PostgreSQL 判定 |
| 向量元数据 | 已有简版 `embedding_models` | 需补 provider/revision/dim/metric 与索引代际契约 |
| 异步事件 | 已有简版 `outbox_events`，缺少 claim、lease、available、dead 等可靠消费字段 | P1 扩展可靠任务模型，P2 落地 worker、重试和对账 |
| Qdrant | 当前无 Qdrant adapter、collection 管理或检索调用 | 新增 `backend/internal/adapter/qdrant`，client 不越过 adapter 边界 |
| Worker | 当前没有 `backend/cmd/vector-worker` | P2 新建独立进程并复用现有优雅停止模式 |
| 会话 | `backend/internal/application/session` 已通过 `ChatAgent` port 接入 AI，并有上下文预算 | P3 新增窄 `KnowledgeRetriever` port，由 composition root 注入 |
| 开发部署 | `docker-compose.yml` 当前只有 PostgreSQL、Redis、backend、frontend | P1 以开发 profile 增加 Qdrant 单节点依赖，生产按需升级为 cluster/Cloud |

## 3. 阶段拆分

目标架构原有四个大阶段在此细化为七个可执行阶段：

| 专项阶段 | 工作文档 | 对应目标架构 | 结果 |
|---|---|---|---|
| P0 开发准备与决策冻结 | [00-development-readiness.md](00-development-readiness.md) | 开发前必须确认的决策 | 冻结范围、SLO、模型、ACL、降级和运维边界 |
| P1 数据与契约基础 | [01-data-and-contract-foundation.md](01-data-and-contract-foundation.md) | MVP 基础部分 | 完成 schema、port、配置、Qdrant adapter 骨架、payload index 和开发环境 |
| P2 入库与向量索引 | [02-ingestion-and-vector-indexing.md](02-ingestion-and-vector-indexing.md) | MVP 入库链路 | 完成上传、解析、切块、嵌入、幂等写入、重试和对账 |
| P3 检索与 RAG 集成 | [03-retrieval-and-rag-integration.md](03-retrieval-and-rag-integration.md) | MVP 检索链路 | 完成混合检索、最终鉴权、引用和 Session 集成，形成 MVP |
| P4 生产就绪 | [04-production-readiness.md](04-production-readiness.md) | 生产化阶段 | 完成生产拓扑、安全、观测、备份、恢复和故障演练 |
| P5 性能与质量 | [05-performance-and-quality.md](05-performance-and-quality.md) | 性能与质量阶段 | 以代表性数据完成索引、吞吐和检索质量调优 |
| P6 多租户、高可用与智能增强 | [06-multitenancy-ha-and-intelligence.md](06-multitenancy-ha-and-intelligence.md) | 多租户、高可用与智能化阶段 | 完成组合权限、隔离、容灾和高级检索能力 |

目标架构的原阶段 1 被拆为 P0-P3；原阶段 2、3、4 分别对应 P4、P5、P6。

## 4. 依赖与里程碑

默认依赖顺序为：

```text
P0 -> P1 -> P2 -> P3 -> P4 -> P5 -> P6
```

P4 的威胁建模、指标设计和运行手册草拟可在 P1-P3 并行准备，但不得在 P3 MVP 未通过前宣告生产就绪。P5 的基线数据应从 P0 开始准备，调优结论必须建立在 P3 的正确性与权限验收之上。P6 不得提前用“未来多租户”放宽 P0-P5 的默认隔离规则。

## 5. 状态与完成规则

阶段和任务只使用以下状态：

| 状态 | 含义 |
|---|---|
| `TODO` | 尚未开始，或仅有方案未形成可验证交付物 |
| `IN_PROGRESS` | 已有负责人和实际产出，仍未满足退出条件 |
| `BLOCKED` | 存在明确阻断项，已记录原因、责任人和解除条件 |
| `DONE` | 所有必做任务、退出条件和验证证据均已完成 |

阶段标记为 `DONE` 前必须同时满足：

1. 阶段任务清单全部完成，或明确记录经批准的移出范围。
2. 阶段验收条件全部有证据，不以代码已合并替代运行验证。
3. 临时测试覆盖公开行为、边界和错误路径，外部依赖使用 Mock；核心新增逻辑目标覆盖率不低于 80%。
4. 按仓库规则删除临时测试源码和测试专用 fixture，只保留命令、结果和覆盖率记录。
5. 相关测试、`go vet`、后端构建、前端 lint/build 及必要运行时 smoke 通过。
6. 安全检查确认日志、错误响应和文档没有密码、Token、密钥或真实连接串。
7. 数据库变化有 forward migration、备份/恢复或补偿方案，部署行为变化已同步当前技术文档。
8. `PROGRESS.md` 的阶段表、决策、风险、证据和更新记录已同步。

## 6. 文档更新规则

1. `PROGRESS.md` 是本专项唯一总状态页；阶段文档保存详细任务和验收口径。
2. 开始阶段时，将阶段和第一项任务改为 `IN_PROGRESS`，记录负责人、开始日期和目标里程碑。
3. 任务完成时在对应阶段文档勾选，并把验证证据写入该文档的完成记录；总页只汇总数量和结论。
4. 架构决策先记录到 P0 决策表和总页决策登记；若改变目标架构，再更新目标架构或新增 ADR。
5. 阻断超过一个工作周期时，记录影响、临时降级、责任人和下一次复核日期。
6. 不在文档中记录真实密钥、Token、密码、内部连接串或受保护数据样本。
7. 开发前再次核对 Git 状态、最高迁移版本和磁盘代码，避免覆盖并行任务结果。

## 7. 每阶段完成记录

每个阶段文档末尾保留同一记录结构：

```text
状态：TODO | IN_PROGRESS | BLOCKED | DONE
负责人：
开始日期：
完成日期：
验证命令：
验证结果：
覆盖率：
交付物：
回滚或降级验证：
遗留风险：
```
