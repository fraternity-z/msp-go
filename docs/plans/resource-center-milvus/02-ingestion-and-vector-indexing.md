# P2 入库与向量索引

> 状态：`TODO`
> 里程碑：M2 入库索引闭环
> 前置依赖：[P1 数据与契约基础](01-data-and-contract-foundation.md) `DONE`
> 后续阶段：[P3 检索与 RAG 集成](03-retrieval-and-rag-integration.md)

## 1. 阶段目标

实现从资源上传到可检索向量的完整异步链路，并证明重复投递、进程崩溃、模型失败、删除、下线和重建最终都能收敛到 PostgreSQL 记录的状态。

目标流程：

```text
上传/登记 -> DRAFT/PROCESSING -> content_version + asset + outbox
-> processing job -> 读取对象 -> 解析 -> 规范化 -> 切块
-> embedding -> Milvus deterministic upsert -> PostgreSQL 发布/失败状态
-> reconcile 持续校正
```

## 2. 预计影响范围

- `backend/internal/application/resource/`：入库编排、状态机、错误分类和对账用例。
- `backend/internal/adapter/postgres/`：版本、任务、outbox、发布/下线的事务实现。
- `backend/internal/adapter/storage/` 与上传模块：受控对象读取和元数据校验。
- 文档解析 adapter：PDF、DOCX、TXT、MD 解析及资源限制。
- embedding adapter：D-002 确认的 provider 和批量契约。
- `backend/internal/adapter/milvus/`：collection ensure、upsert、delete 和 generation 写入。
- `backend/cmd/vector-worker/`：独立 worker 进程、健康检查和优雅停止。
- 资源 HTTP/前端：202、处理状态、失败原因、重试和下线展示。

## 3. 工作清单

- [ ] **P2-01 异步 API 契约**：上传/登记返回 202 和资源/version/job 标识；状态查询返回稳定状态、阶段、可重试性和脱敏错误码。
- [ ] **P2-02 原子登记事务**：在同一 PostgreSQL 事务写入内容、不可变版本、资产、处理任务和 outbox；失败时不留下半成品。
- [ ] **P2-03 独立 worker 入口**：新增 `backend/cmd/vector-worker`，完成配置、信号、健康、优雅停止和结构化日志。
- [ ] **P2-04 任务领取与租约**：实现 `FOR UPDATE SKIP LOCKED` 或等价领取、owner、lease、heartbeat、超时接管和最大并发。
- [ ] **P2-05 受控对象读取**：校验 storage key、MIME、大小、checksum、超时和取消，拒绝任意 URL/路径及过大响应。
- [ ] **P2-06 文档解析**：实现 PDF、DOCX、TXT、MD 解析，处理空文档、扫描 PDF、加密文档、畸形文件、页数/字符限制和明确错误码。
- [ ] **P2-07 文本规范化**：统一换行、Unicode、控制字符、空白和 checksum；保留可追溯页码/段落/标题元数据。
- [ ] **P2-08 确定性切块**：实现稳定 chunk 顺序、边界、重叠、邻接关系和 deterministic chunk ID，同一版本重跑结果一致。
- [ ] **P2-09 Embedding 批处理**：实现批量、超时、取消、有限重试、维度校验、速率/成本限制和脱敏错误。
- [ ] **P2-10 Collection 与 generation 写入**：校验 schema 后写入目标 generation；维度、metric、模型 revision 不一致时失败关闭。
- [ ] **P2-11 幂等 upsert**：以版本/generation/chunk 标识覆盖写入；重复 outbox、重复 job 和崩溃重放不增加重复向量。
- [ ] **P2-12 发布、下线与删除**：只有完整 generation 可发布；下线/删除先更新 PostgreSQL 真相，再异步清理向量，读侧立即以 PostgreSQL 拒绝。
- [ ] **P2-13 重试、dead 与背压**：区分可重试/永久错误，使用有限退避、available time、dead 状态、队列上限和告警，禁止无限热循环。
- [ ] **P2-14 Reconcile 与重建**：实现 PostgreSQL 到 Milvus 的差异扫描、缺失/多余向量修复、generation 重建、进度游标和可重复运维命令。

## 4. 关键不变量

1. PostgreSQL 未发布、已下线或已删除的版本，即使 Milvus 仍有向量也不可被用户读取。
2. 同一 `content_version + generation + chunk_ordinal` 只能对应一个确定性 chunk 标识。
3. job 成功只能发生在全部 chunk 向量写入并校验完成后。
4. job 失败不能把部分 generation 切换为 active。
5. 进程在每个外部副作用前后退出，下一次运行都能重试或对账收敛。
6. 原文、密钥和完整 provider 响应不写入任务错误、日志或 Milvus scalar 字段。
7. 删除向量失败不回滚 PostgreSQL 下线；读侧依赖最终鉴权保证立即不可见。

## 5. 故障注入矩阵

| 故障点 | 预期 |
|---|---|
| 业务事务提交前退出 | 无资源、版本、job 或 outbox 半记录 |
| outbox 已提交、worker 未领取 | 后续轮询可领取 |
| 领取后解析前退出 | lease 到期后可接管 |
| embedding 部分批次失败 | 不发布，按错误类型重试或 dead |
| Milvus upsert 成功、job 完成前退出 | 重放 deterministic upsert，无重复向量 |
| generation 写完、切换前退出 | 旧 generation 继续服务，重放可完成切换 |
| PostgreSQL 已下线、Milvus 删除失败 | 用户立即不可见，清理任务继续重试 |
| reconcile 中断 | 从持久游标或幂等扫描继续 |

## 6. 验证计划

- 临时 application 测试覆盖公开状态机、边界、错误和取消，外部依赖全部使用 Mock。
- 临时 PostgreSQL 集成测试覆盖登记事务、并发领取、租约接管、owner 条件更新、重试、dead 和 outbox 幂等。
- 临时解析测试使用最小安全 fixture 覆盖四种 MIME、空/畸形/超限文件，验证后删除 fixture。
- 临时 Milvus 集成测试覆盖重复 upsert、generation 隔离、删除、schema/维度错误和 reconcile。
- 以故障注入运行上表每个关键点，并记录最终 PostgreSQL/Milvus 状态。
- 核心新增公开逻辑覆盖率目标 80% 以上；记录后删除临时测试源码。
- 运行全量后端测试、vet、build，前端 lint/build，以及 worker/Compose smoke。

## 7. 阶段退出条件

- 支持的四类文档均能从上传进入 active generation，并可追溯到资源、版本、资产、页码和 chunk。
- 重复投递与重启不会产生重复向量，所有故障注入最终收敛。
- 未发布、下线、删除和无权限资源不会因向量残留而可见。
- dead、backlog、处理耗时和 reconcile 差异有指标与告警入口。
- 资源页面能正确展示处理中、可重试失败和已发布状态，不暴露内部错误。
- 重建与清理命令有运行手册、dry-run/范围保护和恢复步骤。

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
| 交付物 | 异步 API、worker、解析/切块/embedding/vector adapters、状态机、reconcile、前端状态 |
| 回滚或降级验证 |  |
| 遗留风险 |  |

