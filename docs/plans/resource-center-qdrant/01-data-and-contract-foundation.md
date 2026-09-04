# P1 数据与契约基础

> 状态：`DONE`
> 里程碑：M1 基础契约就绪
> 前置依赖：[P0 开发准备与决策冻结](00-development-readiness.md) `DONE`
> 后续阶段：[P2 入库与向量索引](02-ingestion-and-vector-indexing.md)
> 模型配置边界：P1 只提供供应商无关 schema/port；实际 embedding 模型由管理员在 P2-A 管理端测试并激活，不由环境变量或代码默认值选定。
> 后续执行门：P2-A 完成后必须暂停，等待管理员确认 active 模型和项目负责人明确继续，方可启动 P2-B。

## 1. 阶段目标

建立 PostgreSQL 数据真相、应用层 ports、Qdrant adapter 边界、配置校验和开发环境。P1 不实现完整文档处理或 RAG，只交付后续阶段可以稳定依赖的契约。

## 2. 预计影响范围

- `backend/migrations/`：按开始开发时的最高版本追加 forward migration，不原地改写已发布迁移。
- `backend/internal/application/resource/`：领域模型、状态机和入库/索引 ports。
- `backend/internal/adapter/postgres/`：资源版本、ACL、任务和 outbox repository。
- `backend/internal/adapter/qdrant/`：唯一允许依赖 Qdrant client 的 adapter。
- `backend/internal/platform/config/`：Qdrant、worker 和检索静态配置及启动校验；embedding 模型运行时选择留给 P2-A 的管理员配置。
- `backend/cmd/api/`：API 侧装配、健康检查和可选能力暴露。
- `docker-compose.yml`、`.env.example` 和相关技术文档：仅开发/测试 Qdrant 单节点配置。

具体文件在编码前再次核对，避免与并行任务产生路径冲突。

## 3. 工作清单

- [x] **P1-01 Schema 基线复核**：确认最高迁移版本为 `0016`，复用 `contents`、`embedding_models` 和 `outbox_events`，新迁移从 `0017` 追加。
- [x] **P1-02 核心实体迁移**：`0017_resource_vector_foundation` 新增租户、知识库、文档/版本/资产/chunk、模型版本、generation、manifest 和可靠任务表，保持 `contents` 为内容根。
- [x] **P1-03 默认租户与知识库**：固定 `default` tenant/kb UUID，现有 `contents` 确定性回填并设置 `tenant_id NOT NULL`；列默认值继续把旧写入链路归入默认租户，同时建立默认 membership。
- [x] **P1-04 版本与资产约束**：加入 checksum、MIME、对象 URI、解析/索引状态、当前版本外键、chunk 顺序/偏移和软删除约束。
- [x] **P1-05 ACL 基础**：新增知识库 subject ACL、allow/deny、有效期及查询索引；保留既有 `content_acl` 兼容读取，PG 作为最终鉴权来源。
- [x] **P1-06 Embedding 与索引代际**：扩展 `embedding_models`，新增不可变 `embedding_model_versions`、`vector_index_generations` 和 `chunk_vector_manifests`，约束 dimension/metric/revision；具体版本由 P2-A 绑定管理员配置的渠道模型并激活。
- [x] **P1-07 可靠任务与 outbox**：新增 `resource_processing_jobs`，扩展 `outbox_events` 的 available、lease、heartbeat、dead、error code、最大重试和幂等键。
- [x] **P1-08 Application ports**：定义对象读取、解析、切块、embedding、vector store、job/outbox、授权和 `KnowledgeRetriever` 的 provider-neutral 窄接口与错误分类。
- [x] **P1-09 配置与启动校验**：加入 Qdrant endpoint/API key、collection、payload index、超时、批量、wait-for-changes 和生产环境密钥校验；错误信息不包含密钥。此项不提供 embedding 模型的环境变量选择。
- [x] **P1-10 Qdrant adapter 骨架**：实现 REST health、collection/schema 校验、payload index、upsert/delete/search、维度校验和脱敏错误映射；不自动修补错误 schema。
- [x] **P1-11 开发 Compose**：以 `vector` profile 加入固定版本 Qdrant 单节点、healthcheck、持久卷和资源限制；默认核心栈不依赖该服务。
- [x] **P1-12 装配与可观测入口**：API 按开关装配 Qdrant，加入详细健康和管理员系统状态；未启用时不创建接口，运行时不可达按 D-008 标为 degraded。

实现说明：P1 只交付契约和连接骨架，不创建未决模型的 collection，也不启动文档处理 worker。P2-A 负责管理员模型测试、激活、不可变版本历史和运行时解析；P2-B 才能消费 active 版本。`VectorIndex` 位于 application 层，Qdrant HTTP 细节仅存在于 `internal/adapter/qdrant`。

## 4. 数据与迁移要求

1. migration 必须 forward-only，执行前准备可恢复 PostgreSQL 备份；已有数据回填要可证明确定性。
2. 表、枚举、约束和索引命名遵循当前 `public` schema 风格。
3. 不把完整正文或动态 ACL 冗余写入 Qdrant；Qdrant 只保留 point ID、版本/generation、最小 payload 和向量，过滤字段必须建立对应 payload index。
4. outbox 与业务状态变化必须处于同一 PostgreSQL 事务。
5. 模型维度、metric 或 revision 不匹配必须明确失败，不能静默截断或混写。
6. 所有时间、租约和重试字段的时区语义必须在 schema 和 Go 类型中一致。

## 5. 验证计划

- 使用临时 migration 测试覆盖空库、当前基线升级、重复执行、约束、默认值和失败回滚。
- 使用 Mock 覆盖 application ports 的成功、边界、超时、取消和错误分类。
- 使用临时 Qdrant collection 验证 vector config、payload index、维度/metric 不匹配、health 和清理；测试结束删除 collection。
- 验证 Qdrant client import 只存在于 adapter 与必要生成代码范围。
- 运行后端定向测试、`go test ./... -count=1`、`go vet ./...`、`go build ./...`。
- 运行 Compose 配置校验和开发 profile 健康 smoke；不把单节点结果宣称为生产验收。
- 按仓库规则记录覆盖率并删除临时测试源码和 fixture。

本次静态/Mock 与实机验证均已完成：后端相关包及全量 `go test`、临时 `httptest`（health、API key header、upsert、schema mismatch）、`go vet`、`go build`、前端 lint/build、`docker compose --profile vector config --quiet` 和 `git diff --check` 均通过；迁移在隔离 PostgreSQL 临时集群中首次应用和重复执行通过。Docker Desktop/WSL2 恢复后，`qdrant/qdrant:v1.14.1` 容器达到 `healthy`，临时 collection 完成 schema、5 个 payload index、确定性重复 upsert、过滤检索、dimension mismatch、按 ID/过滤删除和清理验证；无鉴权开发模式与进程内随机 API key 鉴权模式均通过。实机验证发现并修复了镜像不含 `curl`、空 API key 环境变量会意外开启鉴权，以及 payload index REST 路径/请求体不符合 Qdrant 契约的问题；临时测试源码和 collection 已删除，随机 key 未输出或持久化。

## 6. 阶段退出条件

- 当前数据可通过一条受控 forward migration 进入目标基础 schema，重复迁移无副作用。
- application 层不 import Qdrant client，PostgreSQL/Qdrant/embedding 可通过 ports 单独 Mock。
- 开发环境 Qdrant health 通过，错误配置在启动时给出明确且脱敏的错误。
- outbox/job schema 支持 P2 所需的 claim、lease、heartbeat、retry、dead 和幂等。
- 默认租户/知识库回填、ACL 基础和 generation 约束通过负向测试。
- 技术架构、开发和部署文档已同步实际基础契约。

## 7. 回滚与降级

- 应用回滚前确认旧版本能读取新增 schema；必要时使用兼容窗口而非 down migration。
- migration 失败时停止新应用写入，恢复备份或发布经评审的补偿 migration。
- Qdrant adapter 未配置或健康失败时，按 D-008 保持 PostgreSQL 资源中心可用，并明确禁用向量能力。
- 开发 Qdrant profile 可独立停止和清理，不删除 PostgreSQL 或对象存储数据。

## 8. 完成记录

| 字段 | 内容 |
|---|---|
| 状态 | `DONE` |
| 负责人 | Codex |
| 开始日期 | 2026-09-01 |
| 完成日期 | 2026-09-01 |
| 验证命令 | `go test ./internal/application/resource ./internal/adapter/qdrant ./internal/platform/config ./internal/platform/health ./cmd/api -count=1`；`go test ./... -count=1`；临时 `httptest`/live smoke；`go vet ./...`；`go build ./...`；前端 `npm run lint`/`npm run build`；`docker compose --profile vector config --quiet`；`docker compose --profile vector up -d qdrant` |
| 验证结果 | 相关包/全量测试、临时 adapter smoke、隔离 PostgreSQL 迁移首次/重复执行、`go vet`、`go build`、前端 lint/build、Compose 配置及容器健康均通过；实机 Qdrant 在无鉴权和随机临时 API key 两种模式下完成 collection/schema、5 个 payload index、幂等写入、过滤检索、schema 负向校验、删除与清理，最终临时 collection 数为 0 |
| 覆盖率 | 临时 adapter `httptest` 覆盖率 85.8% statements，覆盖公开操作的成功、边界、旧 API 回退和错误映射；测试源码与 profile 已删除，不纳入仓库 |
| 交付物 | migration、application ports、Qdrant adapter 骨架、配置、Compose、健康装配和技术文档 |
| 回滚或降级验证 | `QDRANT_ENABLED=false` 保持旧启动链；健康/管理员状态仅在配置启用时包含 Qdrant；网络失败映射为 degraded |
| 遗留风险 | D-002/D-004/D-005/D-009/D-010 仍按 P0 计划暂缓；P2-A 可建设管理员配置闭环，但 P2-A 完成后必须暂停，管理员激活实际 embedding 契约且项目负责人明确继续前不得启动 P2-B；P3 前不能宣称业务可检索，P5 前不能宣称质量或性能达标 |
