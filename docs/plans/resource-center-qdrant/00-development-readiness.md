# P0 开发准备与决策冻结

> 状态：`IN_PROGRESS`
> 里程碑：M0 决策与基线就绪
> 前置依赖：无
> 后续阶段：[P1 数据与契约基础](01-data-and-contract-foundation.md)
> 开始日期：2026-08-31

## 1. 阶段目标

在任何生产 schema、Qdrant collection 或 embedding 调用写入前，冻结会改变接口、数据形状、安全和成本的关键决策，并建立可重复的当前基线。P0 只允许小型验证性 spike，不交付对外功能。

## 2. 输入与约束

- 目标设计以 [资源中心 PostgreSQL + Qdrant 双数据库方案](../../technical/resource-center-qdrant-architecture.md) 为准。
- 当前运行行为以 `backend/internal/application/resource`、`backend/internal/adapter/postgres/resource_repository.go`、`backend/internal/application/session`、当前迁移链和部署配置为准。
- PostgreSQL 必须继续作为业务、版本、发布状态和权限的唯一真相。
- MVP 不得宣称已完成完整多租户；没有完整租户模型时使用显式默认租户和默认知识库。
- 所有 spike 产生的 collection、对象、临时测试和数据必须可清理，不得混入生产配置。

## 3. 工作清单

- [x] **P0-01 当前基线清单**：记录资源 API、状态语义、schema、上传/对象存储、Session 上下文、配置和部署拓扑；标出与目标架构的差距。已完成静态基线记录，运行态数据量、查询计划和资源预算仍按第 5 节待补。
- [ ] **P0-02 MIME 与输入边界**：关闭 D-001，确认 PDF、DOCX、TXT、MD 的首批范围，以及文件大小、页数、字符数、批量数、超时和拒绝错误码。
- [ ] **P0-03 Embedding 契约**：关闭 D-002，确认 provider、model key、revision、维度、distance metric、批大小、超时、重试、数据驻留和敏感信息策略。
- [ ] **P0-04 Collection 契约**：关闭 D-003，确认命名格式、共享粒度、vector/payload schema、payload index、alias、shard key/placement、generation 和禁止混写规则。
- [ ] **P0-05 质量基线**：关闭 D-004，冻结代表性语料与查询集、相关性标注方法、Recall@K、MRR/nDCG、答案引用正确率和无答案拒答口径。
- [ ] **P0-06 性能容量基线**：关闭 D-005，定义 10 万至 100 万 chunk 的近期容量档、并发、文档吞吐、检索 P95/P99、worker backlog 和成本预算。
- [ ] **P0-07 一致性与保留**：关闭 D-006，定义 DRAFT/PROCESSING/PUBLISHED/FAILED/ARCHIVED 状态机、强/最终一致性边界、版本与终态任务保留周期。
- [ ] **P0-08 权限矩阵**：关闭 D-007，定义 owner、显式 ACL、角色、默认租户/知识库、deny 优先级、下线/删除和最终鉴权行为。
- [ ] **P0-09 降级矩阵**：关闭 D-008，定义 Qdrant、embedding、rerank、对象存储和 worker 故障的错误契约、FTS 回退、只读模式和恢复条件。
- [ ] **P0-10 备份与容灾目标**：关闭 D-009，确认 PostgreSQL、对象存储、Qdrant 元数据/向量的 RPO、RTO、保留、加密和恢复责任边界。
- [ ] **P0-11 部署拓扑**：关闭 D-010，定义开发单节点、测试环境和生产 Qdrant cluster/Cloud 方案，以及从单节点切换的容量/可用性阈值。
- [ ] **P0-12 API 与 port 草案**：冻结上传 202、处理状态、发布/下线、检索、重试/重建、错误码和 application ports 的最小契约。
- [ ] **P0-13 威胁模型与风险责任人**：覆盖越权检索、payload 泄露、恶意文档、解析炸弹、SSRF、prompt injection、向量投毒和资源耗尽。
- [ ] **P0-14 实施与验证计划**：为 P1-P3 指定负责人、变更顺序、临时测试计划、环境、数据清理、发布/回滚检查点和演示脚本。

## 3.1 已核对的当前事实（2026-08-31）

| 主题 | 证据文件 | 当前事实与差距 |
|---|---|---|
| 资源 API 与发布 | `backend/internal/application/resource/service.go`、`backend/internal/adapter/http/resource/handler.go`、`backend/internal/adapter/postgres/resource_repository.go` | 现有 Repository port 覆盖列表、详情、创建、更新、软删除、收藏和统计；创建接口返回 `201`，数据库插入时直接写入 `PUBLISHED`，没有版本、处理状态或向量索引状态。 |
| PostgreSQL schema | `backend/migrations/0001_initial_schema.up.sql`、`backend/migrations/0016_local_upload_access.up.sql` | 当前规范迁移链最高为 `0016`；已有 `contents`、`content_assets`、`content_acl`、简版 `embedding_models` 和 `outbox_events`，尚无资源版本、chunk、manifest 或可靠向量 job 表。 |
| 配置与部署 | `backend/internal/platform/config/config.go`、`.env.example`、`docker-compose.yml` | 没有 Qdrant/worker 配置或服务；Compose 只有 PostgreSQL、Redis、backend、frontend，P1 前不得假设 Qdrant 已可用。 |
| Session 与 AI 边界 | `backend/internal/application/session`、`backend/cmd/api/main.go` | Session 通过 `ChatAgent` port 使用现有 AI 上下文预算；尚无 `KnowledgeRetriever` port 或检索结果注入。 |
| 可复用运行模式 | `backend/internal/application/wechatreminder/worker.go`、`backend/internal/application/securitylog/cleanup_worker.go`、`backend/internal/platform/health/health.go` | 已有租约/重试/终态清理、`FOR UPDATE SKIP LOCKED` 和依赖健康检查模式，可作为后续 vector-worker 与 Qdrant health adapter 的实现参考。 |

## 3.2 P0 待确认问题（不采用隐含默认值）

下表是进入 P1 前必须由项目负责人及产品、安全、运维相关评审人确认的最小决策矩阵。请逐项回复结论；若暂缓，必须同时给出 `DEFERRED` 理由、不会影响的范围和复核日期。

| 决策 ID | 需要确认的内容 | 当前状态 | 直接影响 |
|---|---|---|---|
| D-001 | 首批 MIME（PDF/DOCX/TXT/MD 等）、单文件大小、页数/字符数、批量数、超时、拒绝错误码，以及是否允许压缩包或扫描 PDF | `OPEN` | 上传 API、解析器、资源耗尽防护 |
| D-002 | embedding provider、model key、revision、维度、distance metric、批量/超时/重试、数据驻留与敏感信息策略 | `OPEN` | `embedding_models`、collection 代际、成本和合规 |
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
| D-002 | provider/model/revision/dim/metric、批量与合规说明 |
| D-003 | collection/alias、payload index、shard key 命名示例、共享与隔离规则 |
| D-004 | 固定语料、查询、标注责任人、离线指标和通过阈值 |
| D-005 | 数据规模、并发、延迟、吞吐、backlog 与成本阈值 |
| D-006 | 状态机、读写一致性、保留和清理时序 |
| D-007 | 权限矩阵、deny 规则、最终鉴权 SQL 责任边界 |
| D-008 | 各依赖故障的用户可见行为、告警和恢复条件 |
| D-009 | 各数据面的 RPO/RTO、备份介质、恢复顺序和演练周期 |
| D-010 | 各环境拓扑、生产选型、升级与切换触发条件 |

## 5. 基线验证

至少记录以下基线，禁止只写“已验证”：

1. 当前资源创建、列表、详情、更新、软删除、收藏和上传流程的 HTTP 状态与数据库变化。
2. 当前 `contents`、`content_assets`、`content_acl`、`embedding_models`、`outbox_events` 的结构与数据量。
3. 当前资源搜索在代表性数据下的查询计划和延迟。
4. 当前 Session 上下文条数、字节预算、AI provider 超时和降级输出。
5. 当前开发 Compose 资源占用，以及增加 Qdrant 单节点的本机资源预算。
6. 当前日志和错误响应的脱敏检查。

外部 provider 和 Qdrant 使用 Mock 完成错误路径验证；live spike 只使用环境变量注入凭据，记录结果后清理临时 collection、对象和测试源码。

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

## 7. 完成记录

| 字段 | 内容 |
|---|---|
| 状态 | `IN_PROGRESS`（P0 文档检查点已交付，阶段门禁未通过） |
| 负责人 | Codex（项目负责人待确认） |
| 开始日期 | 2026-08-31 |
| 完成日期 |  |
| 验证命令 | `go test ./... -count=1`；`go vet ./...`；`go build ./...`；`npm run lint`；`npm run build`；`git diff --check` |
| 验证结果 | Go 与前端基线命令通过；`git diff --check` 通过；Docker CLI 缺失，未执行 Compose/Qdrant smoke。 |
| 覆盖率 | 本检查点仅修改文档，不新增运行时代码；覆盖率不适用。后续阶段按临时测试规则记录，核心新增逻辑目标不低于 80%。 |
| 交付物 | 当前事实清单、D-001～D-010 待确认矩阵、阶段计划、基线命令记录和暂停条件。 |
| 回滚或清理 | 仅文档变更，可通过独立 Git commit 回退；未创建 Qdrant collection、对象、凭据或测试 fixture。 |
| 遗留风险 | D-001～D-010 仍为 `OPEN`；当前创建即 `PUBLISHED` 的兼容策略未批准；本机 Docker/Qdrant smoke 环境不可用。 |
