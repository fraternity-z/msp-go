# P1 数据与契约基础

> 状态：`TODO`
> 里程碑：M1 基础契约就绪
> 前置依赖：[P0 开发准备与决策冻结](00-development-readiness.md) `DONE`
> 后续阶段：[P2 入库与向量索引](02-ingestion-and-vector-indexing.md)

## 1. 阶段目标

建立 PostgreSQL 数据真相、应用层 ports、Qdrant adapter 边界、配置校验和开发环境。P1 不实现完整文档处理或 RAG，只交付后续阶段可以稳定依赖的契约。

## 2. 预计影响范围

- `backend/migrations/`：按开始开发时的最高版本追加 forward migration，不原地改写已发布迁移。
- `backend/internal/application/resource/`：领域模型、状态机和入库/索引 ports。
- `backend/internal/adapter/postgres/`：资源版本、ACL、任务和 outbox repository。
- `backend/internal/adapter/qdrant/`：唯一允许依赖 Qdrant client 的 adapter。
- `backend/internal/platform/config/`：Qdrant、embedding、worker 和检索配置及启动校验。
- `backend/cmd/api/`：API 侧装配、健康检查和可选能力暴露。
- `docker-compose.yml`、`.env.example` 和相关技术文档：仅开发/测试 Qdrant 单节点配置。

具体文件在编码前再次核对，避免与并行任务产生路径冲突。

## 3. 工作清单

- [ ] **P1-01 Schema 基线复核**：重新核对最高迁移版本、现有表/枚举/约束和真实数据兼容性，输出迁移影响表。
- [ ] **P1-02 核心实体迁移**：新增或扩展 `tenants`、`knowledge_bases`、`content_versions`、`document_assets` 等目标实体，保持 `contents` 为内容根。
- [ ] **P1-03 默认租户与知识库**：为现有数据提供确定性回填和非空约束，明确这只是 MVP 兼容层。
- [ ] **P1-04 版本与资产约束**：定义不可变版本、checksum、MIME、存储 key、解析状态、当前版本引用和软删除关系。
- [ ] **P1-05 ACL 基础**：扩展权限主体/范围表达，建立必要索引和最终鉴权所需读模型；实现 D-007 的 MVP 子集。
- [ ] **P1-06 Embedding 与索引代际**：扩展 `embedding_models`，新增 `vector_index_generations`，约束 provider/revision/dim/metric/active generation。
- [ ] **P1-07 可靠任务与 outbox**：新增 `resource_processing_jobs`，扩展 `outbox_events` 的 available、claim、lease、heartbeat、dead、error code 和幂等键。
- [ ] **P1-08 Application ports**：定义对象读取、解析、切块、embedding、vector store、job/outbox、授权和 `KnowledgeRetriever` 的窄接口与错误分类。
- [ ] **P1-09 配置与启动校验**：加入 Qdrant endpoint、API key/TLS、collection、payload index、超时、批量、worker、模型与降级配置；错误信息不得打印密钥。
- [ ] **P1-10 Qdrant adapter 骨架**：实现连接、health、vector/payload schema 与 collection 校验和稳定错误映射；禁止自动用错误维度修补既有 collection。
- [ ] **P1-11 开发 Compose**：以显式 profile 加入 Qdrant 单节点、健康检查、持久卷和资源限制；默认核心栈不被无意改变。
- [ ] **P1-12 装配与可观测入口**：API 只装配所需 port 和健康状态，Qdrant 不可用时按 D-008 决定启动失败或能力降级，并暴露无敏感信息的状态。

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
| 状态 | `TODO` |
| 负责人 | 待定 |
| 开始日期 |  |
| 完成日期 |  |
| 验证命令 |  |
| 验证结果 |  |
| 覆盖率 |  |
| 交付物 | migration、ports、repositories、Qdrant adapter 骨架、配置、Compose、技术文档 |
| 回滚或降级验证 |  |
| 遗留风险 |  |
