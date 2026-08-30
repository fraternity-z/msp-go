# P0 开发准备与决策冻结

> 状态：`TODO`
> 里程碑：M0 决策与基线就绪
> 前置依赖：无
> 后续阶段：[P1 数据与契约基础](01-data-and-contract-foundation.md)

## 1. 阶段目标

在任何生产 schema、Milvus collection 或 embedding 调用写入前，冻结会改变接口、数据形状、安全和成本的关键决策，并建立可重复的当前基线。P0 只允许小型验证性 spike，不交付对外功能。

## 2. 输入与约束

- 目标设计以 [资源中心 PostgreSQL + Milvus 双数据库方案](../../technical/resource-center-milvus-architecture.md) 为准。
- 当前运行行为以 `backend/internal/application/resource`、`backend/internal/adapter/postgres/resource_repository.go`、`backend/internal/application/session`、当前迁移链和部署配置为准。
- PostgreSQL 必须继续作为业务、版本、发布状态和权限的唯一真相。
- MVP 不得宣称已完成完整多租户；没有完整租户模型时使用显式默认租户和默认知识库。
- 所有 spike 产生的 collection、对象、临时测试和数据必须可清理，不得混入生产配置。

## 3. 工作清单

- [ ] **P0-01 当前基线清单**：记录资源 API、状态语义、schema、上传/对象存储、Session 上下文、配置和部署拓扑；标出与目标架构的差距。
- [ ] **P0-02 MIME 与输入边界**：关闭 D-001，确认 PDF、DOCX、TXT、MD 的首批范围，以及文件大小、页数、字符数、批量数、超时和拒绝错误码。
- [ ] **P0-03 Embedding 契约**：关闭 D-002，确认 provider、model key、revision、维度、distance metric、批大小、超时、重试、数据驻留和敏感信息策略。
- [ ] **P0-04 Collection 契约**：关闭 D-003，确认命名格式、共享粒度、collection schema、alias、partition/placement、generation 和禁止混写规则。
- [ ] **P0-05 质量基线**：关闭 D-004，冻结代表性语料与查询集、相关性标注方法、Recall@K、MRR/nDCG、答案引用正确率和无答案拒答口径。
- [ ] **P0-06 性能容量基线**：关闭 D-005，定义 10 万至 100 万 chunk 的近期容量档、并发、文档吞吐、检索 P95/P99、worker backlog 和成本预算。
- [ ] **P0-07 一致性与保留**：关闭 D-006，定义 DRAFT/PROCESSING/PUBLISHED/FAILED/ARCHIVED 状态机、强/最终一致性边界、版本与终态任务保留周期。
- [ ] **P0-08 权限矩阵**：关闭 D-007，定义 owner、显式 ACL、角色、默认租户/知识库、deny 优先级、下线/删除和最终鉴权行为。
- [ ] **P0-09 降级矩阵**：关闭 D-008，定义 Milvus、embedding、rerank、对象存储和 worker 故障的错误契约、FTS 回退、只读模式和恢复条件。
- [ ] **P0-10 备份与容灾目标**：关闭 D-009，确认 PostgreSQL、对象存储、Milvus 元数据/向量的 RPO、RTO、保留、加密和恢复责任边界。
- [ ] **P0-11 部署拓扑**：关闭 D-010，定义开发 Standalone、测试环境和生产 Distributed/托管方案，以及从 Standalone 切换的容量/可用性阈值。
- [ ] **P0-12 API 与 port 草案**：冻结上传 202、处理状态、发布/下线、检索、重试/重建、错误码和 application ports 的最小契约。
- [ ] **P0-13 威胁模型与风险责任人**：覆盖越权检索、payload 泄露、恶意文档、解析炸弹、SSRF、prompt injection、向量投毒和资源耗尽。
- [ ] **P0-14 实施与验证计划**：为 P1-P3 指定负责人、变更顺序、临时测试计划、环境、数据清理、发布/回滚检查点和演示脚本。

## 4. 必须形成的决策记录

| 决策 ID | 最小输出 |
|---|---|
| D-001 | MIME 白名单、限制值、错误码、是否允许压缩包/扫描 PDF |
| D-002 | provider/model/revision/dim/metric、批量与合规说明 |
| D-003 | collection/alias/partition 命名示例、共享与隔离规则 |
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
5. 当前开发 Compose 资源占用，以及增加 Milvus Standalone 的本机资源预算。
6. 当前日志和错误响应的脱敏检查。

外部 provider 和 Milvus 使用 Mock 完成错误路径验证；live spike 只使用环境变量注入凭据，记录结果后清理临时 collection、对象和测试源码。

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
| 状态 | `TODO` |
| 负责人 | 待定 |
| 开始日期 |  |
| 完成日期 |  |
| 验证命令 |  |
| 验证结果 |  |
| 覆盖率 | 不适用或记录 spike 覆盖率 |
| 交付物 | 决策记录、基线数据、权限矩阵、威胁模型、API/port 草案 |
| 回滚或清理 |  |
| 遗留风险 |  |

