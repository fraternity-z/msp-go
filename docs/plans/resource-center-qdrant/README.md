# 资源中心 Qdrant 专项开发计划

> 状态：`IN_PROGRESS`
> 建立日期：2026-08-30
> 当前执行：P0-P3/M3 的开发及测试环境验收全部完成；P4-P6 尚未启动
> 模型配置：管理员已真实激活 `voyage-4-large` 的系统版本 `auto-v2-e5ec9a9f2abaa010`（1024 维、Cosine、`send_dimensions=false`、32/30/3）；运行时只读取已激活的不可变版本，不允许代码、环境变量或普通请求方替代管理员选择。UI 仅要求选择模型，测试自动识别维度，revision 内部生成，高级参数折叠。
> 执行范围：项目负责人已明确解除暂停门，授权在测试环境完成 P2-B、真实模型质量与容量评测；结果、门槛与限制见[验收记录](TEST-ACCEPTANCE-2026-09-06.md)。
> 目标架构：[资源中心 PostgreSQL + Qdrant 双数据库方案](../../technical/resource-center-qdrant-architecture.md)
> 专项总跟踪：[PROGRESS.md](PROGRESS.md)

## 1. 文档定位

本目录把目标架构拆成可执行、可验收、可回退的开发阶段。目标架构文档负责说明最终设计，本目录负责回答“先做什么、做到什么算完成、证据记录在哪里”。

项目级未完成事项仍以 [项目待办](../../TODO.md) 为唯一总入口。本目录只跟踪资源中心 Qdrant 专项，不复制其他模块待办；专项里程碑变化时，应在项目待办保留一条摘要并链接到 `PROGRESS.md`。

当前代码事实以磁盘中的最新实现为准。目标架构与当前实现不一致时，不把目标设计误写成已上线能力。

## 2. 当前基线

下表保留截至 2026-09-04 的历史实现基线；当前 P2-B 已完成，最新状态见第 4.1 节和专项总进度。

| 范围 | 当前事实 | 计划影响 |
|---|---|---|
| 资源业务 | `backend/internal/application/resource` 通过 Repository port 承载列表、详情、创建、更新、软删除、收藏和统计 | 延续 application port 模式，不让业务层依赖 Qdrant client |
| PostgreSQL | `contents` 是内容根，附件位于 `content_assets`，收藏位于 `user_favorites`；`0017` 增加资源向量基础，`0018` 将 embedding 版本绑定到管理员模型并增加单 active 约束 | 保持 PostgreSQL 为业务与权限唯一真相 |
| 发布语义 | 当前教师资源创建后直接写入 `PUBLISHED` | P0 必须决定兼容与迁移策略，目标链路改为异步处理后发布 |
| ACL | 已有 `content_acl`，当前粒度不足以表达目标租户、知识库、用户/部门/角色组合权限 | MVP 先定义默认租户和默认知识库，最终授权仍由 PostgreSQL 判定 |
| 向量元数据 | `0017` 保留并扩展 `embedding_models`，新增含 revision 与向量契约的不可变 `embedding_model_versions` 及 generation/manifest；`0018` 关联 `llm_models`，增加 `send_dimensions`、批量/超时/重试、验证/激活/退役时间和同一逻辑用途唯一 active 索引 | P2-A 管理闭环已完成真实激活；可选 `encoding_format` 会导致当前上游 HTTP 400，省略后完整 active 契约复测成功。P2-B 只在暂停门解除后消费 active 版本建立 collection 和索引代际 |
| 异步事件 | `0017` 新增 `resource_processing_jobs`，并补齐 `outbox_events` claim/lease/available/dead 字段 | P2-B 落地 worker、重试和对账 |
| Qdrant | 已有 `internal/adapter/qdrant` REST adapter、health/schema/payload index/upsert/delete/search 契约，Compose 实机 smoke 已通过；默认配置关闭 | P2-B 仅在管理员激活 embedding 契约且暂停门明确解除后接入业务写入 |
| Worker | 当前没有 `backend/cmd/vector-worker` | P2-B 新建独立进程并复用现有优雅停止模式 |
| 会话 | `backend/internal/application/session` 已通过 `ChatAgent` port 接入 AI，并有上下文预算 | P3 新增窄 `KnowledgeRetriever` port，由 composition root 注入 |
| 开发部署 | `docker-compose.yml` 默认仍为 PostgreSQL、Redis、backend、frontend，`vector` profile 的 Qdrant 单节点已通过无鉴权与 API key 模式实机验证 | P2 继续使用开发 profile，生产按需升级为 cluster/Cloud |

## 3.1 本次执行的阶段计划与验收输出

每个阶段独立修改、独立验证；阶段未满足退出条件时不得宣告通过。P2-B 已于 2026-09-06 完成，M3 的真实质量和容量必须按事先冻结的门槛验收。

| 阶段 | 目标 | 主要文件/模块 | 验证方式 | 预期产出与阶段门禁 |
|---|---|---|---|---|
| P0 | 记录当前事实，冻结模型管理责任、集合、权限、降级和运维边界 | 本目录 P0 文档、`docs/technical/development.md`、`docs/TODO.md` | 文档交叉核对、`git diff --check`、现有 Go/前端基线命令；记录 Docker/Qdrant 环境阻断 | 决策登记、基线证据、风险与实施顺序；D-001～D-010 全部 `DECIDED` 或合规 `DEFERRED` 后才过门 |
| P1 | 建立可回退的 schema、application port、配置和 Qdrant adapter 边界 | `backend/migrations/`、`backend/internal/application/`、`backend/internal/adapter/qdrant/`、配置/Compose/健康检查 | Mock 契约测试、迁移复跑、`go test`/`go vet`/`go build`、可用环境下 Compose smoke | 基础契约和开发 Qdrant 可用；无业务写入和完整 RAG |
| P2-A | 建立管理员控制的 embedding 模型测试、激活、版本历史和运行时解析闭环 | `adminaiconfig` application/HTTP/PostgreSQL、管理端 AI 模型设置、embedding version migration | Mock 与真实 live probe、迁移复跑、权限负向验证、后端 test/vet/build、前端 lint/build | 唯一 active 不可变模型契约通过；曾按门禁暂停，2026-09-06 已明确解除 |
| P2-B | 打通上传到向量索引的异步、幂等、可重试闭环 | resource service/repository、outbox/job、`backend/cmd/vector-worker`、解析/embedding/Qdrant adapter | 临时测试覆盖公共/边界/错误路径，崩溃重启与重复投递回放，对账和删除验证 | 已完成四格式真实入库、幂等发布、失败重试、对账重建及保留清理 |
| P3 | 提供混合检索、最终鉴权、引用和 Session RAG MVP | retrieval application port、PostgreSQL FTS、Qdrant adapter、session 组合根、HTTP 契约 | 权限负向测试、RRF/降级验证、引用一致性、端到端 smoke | 检索结果不越权，Qdrant 故障可按 D-008 回退，MVP 门通过 |
| P4 | 完成生产拓扑、安全、观测、备份恢复和故障演练 | 部署/运维文档、指标告警、备份脚本、恢复与重建流程 | 生产样拓扑演练、故障注入、RPO/RTO 和安全扫描 | 生产就绪门通过，形成可操作运行手册 |
| P5 | 在批准规模上验证性能和检索质量 | 基准脚本、评测集、索引参数和查询配置 | 固定负载 P95/P99/QPS、backlog、Recall/MRR/nDCG、引用正确率 | 达到 P0 批准的 SLO/阈值，结论可复现 |
| P6 | 扩展多租户、高可用、模型切换和智能增强 | ACL/租户模型、拓扑、generation/alias、rerank/查询扩展 | 隔离与容灾演练、模型切换、跨租户负向测试 | 高级能力验收通过；不改变前序阶段的默认隔离承诺 |

## 3. 阶段拆分

目标架构原有四个大阶段在此细化为七个可执行阶段：

| 专项阶段 | 工作文档 | 对应目标架构 | 结果 |
|---|---|---|---|
| P0 开发准备与决策冻结 | [00-development-readiness.md](00-development-readiness.md) | 开发前必须确认的决策 | 冻结范围、SLO、模型、ACL、降级和运维边界 |
| P1 数据与契约基础 | [01-data-and-contract-foundation.md](01-data-and-contract-foundation.md) | MVP 基础部分 | 完成 schema、port、配置、Qdrant adapter 骨架、payload index 和开发环境 |
| P2 管理员模型配置、入库与向量索引 | [02-ingestion-and-vector-indexing.md](02-ingestion-and-vector-indexing.md) | MVP 入库链路 | 19/19 已完成：模型管理、上传、解析、切块、嵌入、幂等写入、重试、对账和清理 |
| P3 检索与 RAG 集成 | [03-retrieval-and-rag-integration.md](03-retrieval-and-rag-integration.md) | MVP 检索链路 | 完成混合检索、最终鉴权、引用和 Session 集成，形成 MVP |
| P4 生产就绪 | [04-production-readiness.md](04-production-readiness.md) | 生产化阶段 | 完成生产拓扑、安全、观测、备份、恢复和故障演练 |
| P5 性能与质量 | [05-performance-and-quality.md](05-performance-and-quality.md) | 性能与质量阶段 | 以代表性数据完成索引、吞吐和检索质量调优 |
| P6 多租户、高可用与智能增强 | [06-multitenancy-ha-and-intelligence.md](06-multitenancy-ha-and-intelligence.md) | 多租户、高可用与智能化阶段 | 完成组合权限、隔离、容灾和高级检索能力 |

目标架构的原阶段 1 被拆为 P0-P3；原阶段 2、3、4 分别对应 P4、P5、P6。

## 4. 依赖与里程碑

默认依赖顺序为：

```text
P0 -> P1 -> P2-A -> 强制暂停 -> P2-B -> P3 -> P4 -> P5 -> P6
```

P2-A 完成只代表模型配置能力通过，暂停门已由项目负责人于 2026-09-06 明确解除并完成 P2-B。P4 不得在 P3 MVP 未通过前宣告生产就绪；P5 调优结论必须建立在正确性、权限和代表性数据验收之上。P6 不得提前放宽默认隔离规则。

## 4.1 当前执行边界

P0-P3/M3 的开发及测试环境验收已完成。60 篇原创材料经真实入库，最终 100 条冻结查询的三个质量指标均为 1.0，500 次引用核验通过。10 万条/5 并发本地与完整检索 P95 为 427/622 ms，达到冻结门槛；百万档趋势评测完成并记录降级。明确授权后的 5 次真实 Tutor 无答案问答全部成功，逐条核对无错误资料引用或来源冒认；输入与测试替身边界详见[本轮验收](TEST-ACCEPTANCE-2026-09-06.md)。当前 P0-P3 无未完成开发或验收项；生产拓扑、容灾和完整多租户仍属于 P4-P6。

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
9. P2-A 是显式人工检查点；必须记录解除暂停的指令。本次已由项目负责人于 2026-09-06 明确授权测试环境 P2-B 与真实质量、容量评测。

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
