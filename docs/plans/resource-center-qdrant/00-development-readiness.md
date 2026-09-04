# P0 开发准备与决策冻结

> 状态：`DONE`
> 里程碑：M0 决策与基线就绪
> 前置依赖：无
> 后续阶段：[P1 数据与契约基础](01-data-and-contract-foundation.md)
> 开始日期：2026-08-31
> 完成日期：2026-09-01
> 模型配置补充决策：实际 embedding 模型由管理员在管理端测试并激活；代码、环境变量和普通请求方不得替代管理员选择。
> 后续执行门：P2-A 完成后必须暂停，等待管理员确认 active 模型和项目负责人明确继续，方可启动 P2-B。

## 1. 阶段目标

在任何生产 schema、Qdrant collection 或 embedding 调用写入前，冻结会改变接口、数据形状、安全和成本的关键决策，并建立可重复的当前基线。P0 只允许小型验证性 spike，不交付对外功能。

## 2. 输入与约束

- 目标设计以 [资源中心 PostgreSQL + Qdrant 双数据库方案](../../technical/resource-center-qdrant-architecture.md) 为准。
- 当前运行行为以 `backend/internal/application/resource`、`backend/internal/adapter/postgres/resource_repository.go`、`backend/internal/application/session`、当前迁移链和部署配置为准。
- PostgreSQL 必须继续作为业务、版本、发布状态和权限的唯一真相。
- MVP 不得宣称已完成完整多租户；没有完整租户模型时使用显式默认租户和默认知识库。
- Embedding provider、model、revision、dimension、metric 及运行参数由管理员通过管理端配置、测试并激活；运行时只消费 active 不可变版本，不保留代码或环境变量模型默认值。
- 所有 spike 产生的 collection、对象、临时测试和数据必须可清理，不得混入生产配置。

## 3. 工作清单

- [x] **P0-01 当前基线清单**：记录资源 API、状态语义、schema、上传/对象存储、Session 上下文、配置和部署拓扑；标出与目标架构的差距。已完成静态基线记录，运行态数据量、查询计划和资源预算仍按第 5 节待补。
- [x] **P0-02 MIME 与输入边界**：关闭 D-001，首批支持 PDF、DOCX、TXT、MD；单文件 50 MiB、200 页、2,000,000 字符，单批 10 个文件，解析预算 120 秒；拒绝压缩包和扫描 PDF，并使用稳定的大小/类型/解析错误码。
- [x] **P0-03 Embedding 契约**：D-002 的实际取值暂缓到管理员配置；先冻结供应商无关的 provider/model/revision/dimension/metric 字段和启用条件，由 P2-A 提供管理端测试、原子激活和版本历史，不在未激活模型时创建 collection 或调用外部服务。
- [x] **P0-04 Collection 契约**：关闭 D-003，采用按场景与模型族共享的 dense collection、COSINE、generation 隔离和强制 payload index；MVP 不启用 custom shard key，也不允许请求方选择 collection。
- [x] **P0-05 质量基线**：D-004 暂缓到 2026-09-08；P1/P2 只做契约、安全和可执行性验证，未形成标注集前不宣称 Recall/MRR/nDCG 或引用质量达标。
- [x] **P0-06 性能容量基线**：D-005 暂缓到 2026-09-08；先保留 10 万至 100 万 chunk 的容量档和测量字段，未有代表性负载前不修改生产参数或宣称 SLO 达标。
- [x] **P0-07 一致性与保留**：关闭 D-006，新版本必须经过 PROCESSING/READY 后才能发布；PostgreSQL 对发布、ACL 和删除强一致，Qdrant 写入/清理最终一致；旧 generation 保留 7 天，终态任务保留 30 天。
- [x] **P0-08 权限矩阵**：关闭 D-007，MVP 使用固定 `default` tenant/knowledge base，owner 与 `content_acl` 共同决定读取，deny 优先；Qdrant 只做粗过滤，PostgreSQL 批量最终鉴权，完整角色/部门模型留到 P6。
- [x] **P0-09 降级矩阵**：关闭 D-008，Qdrant 或 query embedding 故障时允许 FTS-only 并标记 degraded，rerank 故障回退融合结果，PostgreSQL 鉴权故障 fail closed，文档处理故障不发布新版本。
- [x] **P0-10 备份与容灾目标**：D-009 暂缓到 2026-09-15；P4 前不宣称生产 RPO/RTO，P1 仅保证 collection 可清理、manifest 可重建，生产备份责任和保留期由运维评审确认。
- [x] **P0-11 部署拓扑**：D-010 暂缓到 2026-09-15；开发/集成固定使用单节点 Qdrant profile，生产切换 cluster/Cloud 的触发点暂定为超过 1M vectors、峰值 QPS 20 或明确 HA 要求，正式阈值进入 P4 评审。
- [x] **P0-12 API 与 port 草案**：冻结 202 上传、状态查询、发布/下线、检索、重试/重建和错误码草案，应用层只暴露 `VectorIndex`、`KnowledgeRetriever`、`DocumentParser`、`EmbeddingProvider` 等窄接口。
- [x] **P0-13 威胁模型与风险责任人**：完成越权检索、payload 泄露、恶意文档、解析炸弹、SSRF、prompt injection、向量投毒和资源耗尽的责任边界记录，详见风险登记。
- [x] **P0-14 实施与验证计划**：冻结 P1-P3 的变更顺序、临时测试清理规则、Docker/Qdrant 环境要求、发布/回滚检查点和证据记录方式。

## 3.1 P0 冻结时核对的事实（2026-08-31）

以下表格是 P0 的历史快照；P1 已追加 `0017`、application ports、Qdrant adapter 和可选 Compose profile，当前状态以 [P1 阶段文档](01-data-and-contract-foundation.md) 与 [专项进度](PROGRESS.md) 为准。

| 主题 | 证据文件 | 当前事实与差距 |
|---|---|---|
| 资源 API 与发布 | `backend/internal/application/resource/service.go`、`backend/internal/adapter/http/resource/handler.go`、`backend/internal/adapter/postgres/resource_repository.go` | 现有 Repository port 覆盖列表、详情、创建、更新、软删除、收藏和统计；创建接口返回 `201`，数据库插入时直接写入 `PUBLISHED`，没有版本、处理状态或向量索引状态。 |
| PostgreSQL schema | `backend/migrations/0001_initial_schema.up.sql`、`backend/migrations/0016_local_upload_access.up.sql` | 当前规范迁移链最高为 `0016`；已有 `contents`、`content_assets`、`content_acl`、简版 `embedding_models` 和 `outbox_events`，尚无资源版本、chunk、manifest 或可靠向量 job 表。 |
| 配置与部署 | `backend/internal/platform/config/config.go`、`.env.example`、`docker-compose.yml` | 没有 Qdrant/worker 配置或服务；Compose 只有 PostgreSQL、Redis、backend、frontend，P1 前不得假设 Qdrant 已可用。 |
| Session 与 AI 边界 | `backend/internal/application/session`、`backend/cmd/api/main.go` | Session 通过 `ChatAgent` port 使用现有 AI 上下文预算；尚无 `KnowledgeRetriever` port 或检索结果注入。 |
| 可复用运行模式 | `backend/internal/application/wechatreminder/worker.go`、`backend/internal/application/securitylog/cleanup_worker.go`、`backend/internal/platform/health/health.go` | 已有租约/重试/终态清理、`FOR UPDATE SKIP LOCKED` 和依赖健康检查模式，可作为后续 vector-worker 与 Qdrant health adapter 的实现参考。 |

## 3.2 P0 决策问题清单与评审边界

下表保留进入 P1 前需要覆盖的最小决策面。本次结论见 4.1；后续评审只需处理标记为 `DEFERRED` 的事项，不得用隐含默认值扩大范围。

表格中的 `OPEN` 仅表示决策冻结前的原始记录，当前有效状态和复核日期以 4.1 及专项总进度为准。

| 决策 ID | 需要确认的内容 | 当前状态 | 直接影响 |
|---|---|---|---|
| D-001 | 首批 MIME（PDF/DOCX/TXT/MD 等）、单文件大小、页数/字符数、批量数、超时、拒绝错误码，以及是否允许压缩包或扫描 PDF | `OPEN` | 上传 API、解析器、资源耗尽防护 |
| D-002 | 由管理员配置的 embedding provider、model key、revision、维度、distance metric、批量/超时/重试、数据驻留与敏感信息策略 | `OPEN` | 管理端 AI 模型设置、`embedding_models`、collection 代际、成本和合规 |
| D-003 | collection/alias 命名、共享粒度、vector/payload schema、payload index、shard/placement、generation 和禁止混写规则 | `OPEN` | Qdrant adapter、迁移和重建 |
| D-004 | 代表性语料与查询集、标注责任人、Recall@K、MRR/nDCG、引用正确率和无答案拒答阈值 | `OPEN` | P3 正确性与 P5 质量验收 |
| D-005 | 10 万～100 万 chunk 容量档、并发、文档吞吐、检索 P95/P99、worker backlog 和成本预算 | `OPEN` | worker 并发、索引参数和扩容阈值 |
| D-006 | DRAFT/PROCESSING/PUBLISHED/FAILED/ARCHIVED 状态机、强/最终一致性边界、版本与终态任务保留周期 | `OPEN` | 当前“创建即 PUBLISHED”兼容路径、清理和重建 |
| D-007 | owner/ACL/角色/默认租户与知识库、deny 优先级、下线/删除行为和 PostgreSQL 最终鉴权规则 | `OPEN` | payload 过滤、检索上下文和越权防护 |
| D-008 | Qdrant、embedding、rerank、对象存储或 worker 故障时的错误契约、FTS 回退、只读模式和恢复条件 | `OPEN` | 用户可见错误、告警和降级 |
| D-009 | PostgreSQL、对象存储、Qdrant 元数据/向量的 RPO/RTO、保留、加密、备份介质和恢复责任边界 | `OPEN` | 生产备份、灾备和演练 |
| D-010 | 开发单节点、测试环境、生产 Qdrant cluster/Cloud 拓扑，以及单节点切换的容量/可用性阈值 | `OPEN` | Compose profile、部署和采购 |

## 4. 必须形成的决策记录

| 决策 ID | 最小输出 |
|---|---|
| D-001 | MIME 白名单、限制值、错误码、是否允许压缩包/扫描 PDF |
| D-002 | 管理员测试并激活的 provider/model/revision/dim/metric、批量与合规说明，以及非敏感激活证据 |
| D-003 | collection/alias、payload index、shard key 命名示例、共享与隔离规则 |
| D-004 | 固定语料、查询、标注责任人、离线指标和通过阈值 |
| D-005 | 数据规模、并发、延迟、吞吐、backlog 与成本阈值 |
| D-006 | 状态机、读写一致性、保留和清理时序 |
| D-007 | 权限矩阵、deny 规则、最终鉴权 SQL 责任边界 |
| D-008 | 各依赖故障的用户可见行为、告警和恢复条件 |
| D-009 | 各数据面的 RPO/RTO、备份介质、恢复顺序和演练周期 |
| D-010 | 各环境拓扑、生产选型、升级与切换触发条件 |

## 4.1 本次冻结的工程决策（2026-09-01）

以下结论是为启动 P1 采用的可回退工程基线。标记为 `DEFERRED` 的事项已写明复核日期；在对应复核完成前，不得把后续阶段的质量、容量或生产能力宣称为已验收。

| 决策 | 状态 | 本次结论与复核边界 |
|---|---|---|
| D-001 | `DECIDED` | PDF/DOCX/TXT/MD；50 MiB、200 页、200 万字符、单批 10 个、解析 120 秒；不接收压缩包或扫描 PDF。 |
| D-002 | `DEFERRED` | 采用 OpenAI-compatible 供应商无关适配器；provider/model/revision/dimension/metric、批量、超时和重试由管理员在管理端测试并激活，不写死于代码或环境变量。P2-A 可先实现管理闭环；管理员确认实际模型、合规和费用前不得进入 P2-B。 |
| D-003 | `DECIDED` | `resource_chunks_dense_<model_family>_v1` 共享 collection，COSINE dense vector；tenant、knowledge base、version、model、generation、projected state 建 payload index；MVP 无 alias/custom shard。 |
| D-004 | `DEFERRED` | 2026-09-08 前提供带权限边界的中文/英文/数学代表性语料、查询集和标注人；此前只验收安全与契约，不发布召回指标。 |
| D-005 | `DEFERRED` | 2026-09-08 前补充容量、并发、P95/P99、吞吐、backlog 和成本预算；开发小数据只用于可执行性验证。 |
| D-006 | `DECIDED` | PostgreSQL 状态和授权强一致，Qdrant 最终一致；READY 才能发布，软删除/下线立即由 PG 阻断读取，旧 generation 7 天、终态任务 30 天。 |
| D-007 | `DECIDED` | 固定 `default` tenant/knowledge base；owner + content ACL，deny 优先；所有候选必须 PG 最终复核，客户端不能覆盖租户或 collection。 |
| D-008 | `DECIDED` | Qdrant/query embedding 失败走 FTS-only + degraded，rerank 失败保留融合结果，PG 鉴权失败 fail closed，处理失败不发布新版本。 |
| D-009 | `DEFERRED` | 2026-09-15 前由运维确认 PostgreSQL、对象存储、Qdrant 的 RPO/RTO、快照加密和保留；P4 前不宣称生产灾备。 |
| D-010 | `DEFERRED` | 开发/集成使用固定版本单节点 profile；超过 1M vectors、峰值 QPS 20 或 HA 要求时进入 cluster/Cloud 评审，2026-09-15 复核。 |

## 5. 基线验证

至少记录以下基线，禁止只写“已验证”：

1. 当前资源创建、列表、详情、更新、软删除、收藏和上传流程的 HTTP 状态与数据库变化。
2. 当前 `contents`、`content_assets`、`content_acl`、`embedding_models`、`outbox_events` 的结构与数据量。
3. 当前资源搜索在代表性数据下的查询计划和延迟。
4. 当前 Session 上下文条数、字节预算、AI provider 超时和降级输出。
5. 当前开发 Compose 资源占用，以及增加 Qdrant 单节点的本机资源预算。
6. 当前日志和错误响应的脱敏检查。

外部 provider 和 Qdrant 使用 Mock 完成错误路径验证；embedding live probe 只使用管理员在管理端维护的渠道凭据和模型，文档、命令与日志不记录凭据。验证后清理临时 collection、对象和测试源码。

### 5.1 本次已执行的基线命令

| 命令 | 结果 | 说明 |
|---|---|---|
| `Set-Location backend; go test ./... -count=1` | 通过 | 当前后端全量包测试通过；仓库没有永久测试文件。 |
| `Set-Location backend; go vet ./...` | 通过 | 无输出。 |
| `Set-Location backend; go build ./...` | 通过 | 后端构建通过。 |
| `Set-Location frontend; npm run lint` | 通过 | 仅记录既有工具链提示。 |
| `Set-Location frontend; npm run build` | 通过 | 有既有 Browserslist 过期和大 chunk 警告，不影响本次文档检查点。 |
| `docker compose config --quiet` | 未执行（环境阻断） | 本机未安装 Docker CLI，因此 Compose/Qdrant smoke 不能宣称通过。 |

## 6. 阶段退出条件

- D-001 至 D-010 全部为 `DECIDED`，或有不影响 P1 的明确 `DEFERRED` 记录。
- API 草案、状态机、application ports、collection schema 和错误码之间无冲突。
- 代表性语料、查询集、权限矩阵和容量档可由后续阶段直接复用。
- “当前创建即 PUBLISHED”的兼容和迁移路径已批准。
- 默认租户/知识库不会被误解为完整多租户能力。
- 威胁、成本、RPO/RTO 和降级均有负责人。
- `PROGRESS.md` 已同步任务数、决策、风险和下一阶段启动条件。
- P2-A 只负责管理员模型配置闭环；P2-A 完成后强制暂停，不把完成状态解释为 P2-B 启动授权。

## 7. 完成记录

| 字段 | 内容 |
|---|---|
| 状态 | `DONE`（允许进入 P1；D-002、D-004、D-005、D-009、D-010 按记录暂缓） |
| 负责人 | Codex（项目负责人待确认） |
| 开始日期 | 2026-08-31 |
| 完成日期 | 2026-09-01 |
| 验证命令 | `go test ./... -count=1`；`go vet ./...`；`go build ./...`；`npm run lint`；`npm run build`；`git diff --check`；`docker --version`；`docker compose version` |
| 验证结果 | Go 与前端基线命令通过；`git diff --check` 通过；Docker CLI 未安装，Compose/Qdrant smoke 无法执行，已记录为 P1 运行验证阻断。 |
| 覆盖率 | 本检查点仅修改文档，不新增运行时代码；覆盖率不适用。后续阶段按临时测试规则记录，核心新增逻辑目标不低于 80%。 |
| 交付物 | 当前事实清单、D-001～D-010 决策登记、MVP 状态/权限/降级边界、阶段计划、基线命令记录和 P1 启动条件。 |
| 回滚或清理 | 仅文档变更，可通过本阶段 Git commit 回退；未创建 Qdrant collection、对象、凭据或测试 fixture。 |
| 遗留风险 | 五项决策按日期暂缓；D-002 的实际模型由管理员在 P2-A 管理端闭环中测试并激活；P2-A 完成后仍须暂停并等待明确继续指令。当前资源创建即 `PUBLISHED` 的兼容迁移将在 P2-B 实现。Docker/Qdrant 在 P0 执行时不可用，运行时随后已恢复并在 P1 完成实机 smoke。 |
