# P2 入库与向量索引

> 状态：`IN_PROGRESS`
> 里程碑：M2-A 管理员模型配置就绪 / M2-B 入库索引闭环
> 前置依赖：[P1 数据与契约基础](01-data-and-contract-foundation.md) `DONE`
> 后续阶段：[P3 检索与 RAG 集成](03-retrieval-and-rag-integration.md)
> 当前子阶段：P2 强制暂停（P2-A/M2-A 已完成）
> 模型配置：Embedding 模型由管理员在管理端测试并激活；当前 active 为 `voyage-4-large` 的系统版本 `auto-v2-e5ec9a9f2abaa010`（1024 维、Cosine、`send_dimensions=false`、32/30/3），运行时只读取唯一 active 的不可变版本，不允许代码、环境变量或普通请求方覆盖。
> 执行暂停：P2-A 验证完成后已暂停；D-002 的费用与数据合规仍待确认，Qdrant 当前不可用。D-002 决策和项目负责人明确继续前，不得启动 P2-B 或任何后续阶段。

## 1. 阶段目标

先以 P2-A 建立管理员控制的 embedding 模型配置、验证、激活和运行时解析闭环；P2-A 完成后强制暂停。只有暂停门明确解除，P2-B 才实现从资源上传到可检索向量的完整异步链路，并证明重复投递、进程崩溃、模型失败、删除、下线和重建最终都能收敛到 PostgreSQL 记录的状态。

P2-B 目标流程：

```text
上传/登记 -> DRAFT/PROCESSING -> content_version + asset + outbox
-> processing job -> 读取对象 -> 解析 -> 规范化 -> 切块
-> embedding -> Qdrant deterministic point upsert -> PostgreSQL 发布/失败状态
-> reconcile 持续校正
```

## 2. 预计影响范围

- `backend/internal/application/resource/`：入库编排、状态机、错误分类和对账用例。
- `backend/internal/application/adminaiconfig/`、对应 HTTP/PostgreSQL adapter 与 migration：管理员模型测试、激活、不可变版本和运行时解析。
- 管理端 AI 模型设置：选择已启用渠道模型，配置向量契约，执行测试并激活，查看版本历史。
- `backend/internal/adapter/postgres/`：版本、任务、outbox、发布/下线的事务实现。
- `backend/internal/adapter/storage/` 与上传模块：受控对象读取和元数据校验。
- 文档解析 adapter：PDF、DOCX、TXT、MD 解析及资源限制。
- embedding adapter：只消费管理员激活的 D-002 provider/model/version 和批量契约。
- `backend/internal/adapter/qdrant/`：collection/payload index ensure、point upsert、delete 和 generation 写入。
- `backend/cmd/vector-worker/`：独立 worker 进程、健康检查和优雅停止。
- 资源 HTTP/前端：202、处理状态、失败原因、重试和下线展示。

## 3. 工作清单

### 3.1 P2-A 管理员模型配置

- [x] **P2-A-01 管理责任与契约**：复用管理员维护的渠道和模型，配置 revision、dimension、metric、tokenizer/normalization、max tokens、批量、超时和重试；普通用户与业务请求不能选择模型。
- [x] **P2-A-02 不可变版本迁移**：将模型版本关联到管理员模型，保证同一逻辑用途最多一个 active 版本；自动 revision 绑定渠道/API base、模型来源版本和完整向量/运行契约，显式 revision 不允许覆盖不同来源或参数；激活新版本时原子退役旧版本，渠道凭据继续加密存储且不复制到版本记录。
- [x] **P2-A-03 管理端 API**：提供版本列表、受控 `/v1/embeddings` 测试和验证后激活；校验响应顺序与实际维度，多 Key 渠道逐 Key 验证并对网络错误、HTTP 408/429、5xx 有限退避，整次验证最多追加 20 次重试；错误和响应不暴露 API key 或上游正文，界面提示真实调用可能产生费用。
- [x] **P2-A-04 管理端界面**：管理员可选择已启用渠道模型、编辑向量契约、测试连接、验证并激活及查看历史；未配置状态明确可见。
- [x] **P2-A-05 运行时与验收**：运行时只解析管理员激活版本；无 active 版本、渠道/模型停用或契约漂移时向量能力失败关闭。完成迁移、Mock/真实 live probe、权限负向、后端 test/vet/build、前端 lint/build 和文档验证后记录证据。当前上游对可选 `encoding_format` 返回 HTTP 400，省略后按完整 active 契约真实复测成功。

P2-A 全部通过后立即暂停。暂停只允许补充证据和记录，不得开始以下 P2-B 任务。

### 3.2 P2-B 入库索引闭环（暂停解除后）

- [ ] **P2-B-01 异步 API 契约**：上传/登记返回 202 和资源/version/job 标识；状态查询返回稳定状态、阶段、可重试性和脱敏错误码。
- [ ] **P2-B-02 原子登记事务**：在同一 PostgreSQL 事务写入内容、不可变版本、资产、处理任务和 outbox；失败时不留下半成品。
- [ ] **P2-B-03 独立 worker 入口**：新增 `backend/cmd/vector-worker`，完成配置、信号、健康、优雅停止和结构化日志。
- [ ] **P2-B-04 任务领取与租约**：实现 `FOR UPDATE SKIP LOCKED` 或等价领取、owner、lease、heartbeat、超时接管和最大并发。
- [ ] **P2-B-05 受控对象读取**：校验 storage key、MIME、大小、checksum、超时和取消，拒绝任意 URL/路径及过大响应。
- [ ] **P2-B-06 文档解析**：实现 PDF、DOCX、TXT、MD 解析，处理空文档、扫描 PDF、加密文档、畸形文件、页数/字符限制和明确错误码。
- [ ] **P2-B-07 文本规范化**：统一换行、Unicode、控制字符、空白和 checksum；保留可追溯页码/段落/标题元数据。
- [ ] **P2-B-08 确定性切块**：实现稳定 chunk 顺序、边界、重叠、邻接关系和 deterministic chunk ID，同一版本重跑结果一致。
- [ ] **P2-B-09 Embedding 批处理**：使用管理员激活模型实现批量、超时、取消、有限重试、维度校验、速率/成本限制和脱敏错误。
- [ ] **P2-B-10 Collection 与 generation 写入**：校验 vector config、payload index 和目标 generation；维度、metric、模型 revision 不一致时失败关闭。
- [ ] **P2-B-11 幂等 upsert**：以确定性 point ID 和版本/generation/chunk payload 覆盖写入；重复 outbox、重复 job 和崩溃重放不增加重复 point。
- [ ] **P2-B-12 发布、下线与删除**：只有完整 generation 可发布；下线/删除先更新 PostgreSQL 真相，再异步清理向量，读侧立即以 PostgreSQL 拒绝。
- [ ] **P2-B-13 重试、dead 与背压**：区分可重试/永久错误，使用有限退避、available time、dead 状态、队列上限和告警，禁止无限热循环。
- [ ] **P2-B-14 Reconcile 与重建**：通过 point ID、payload 和 count 实现 PostgreSQL 到 Qdrant 的差异扫描、缺失/多余 point 修复、generation 重建、进度游标和可重复运维命令。

## 4. 关键不变量

1. PostgreSQL 未发布、已下线或已删除的版本，即使 Qdrant 仍有向量也不可被用户读取。
2. 同一 `content_version + generation + chunk_ordinal` 只能对应一个确定性 chunk 标识。
3. job 成功只能发生在全部 chunk 向量写入并校验完成后。
4. job 失败不能把部分 generation 切换为 active。
5. 进程在每个外部副作用前后退出，下一次运行都能重试或对账收敛。
6. 原文、密钥和完整 provider 响应不写入任务错误、日志或 Qdrant payload。
7. 删除向量失败不回滚 PostgreSQL 下线；读侧依赖最终鉴权保证立即不可见。
8. 模型选择权只属于管理员；worker、API、上传者和检索请求均不得传入或覆盖 provider、model、dimension 或 metric。
9. 无 active 管理员模型配置时不创建 collection、不调用 embedding provider、不启动 P2-B 向量处理，并返回明确且脱敏的不可用状态。

## 5. 故障注入矩阵

| 故障点 | 预期 |
|---|---|
| 无 active 模型或关联渠道/模型已停用 | 不创建 collection、不调用 provider，向量能力失败关闭并提示管理员配置 |
| 业务事务提交前退出 | 无资源、版本、job 或 outbox 半记录 |
| outbox 已提交、worker 未领取 | 后续轮询可领取 |
| 领取后解析前退出 | lease 到期后可接管 |
| embedding 部分批次失败 | 不发布，按错误类型重试或 dead |
| Qdrant upsert 成功、job 完成前退出 | 重放 deterministic upsert，无重复向量 |
| generation 写完、切换前退出 | 旧 generation 继续服务，重放可完成切换 |
| PostgreSQL 已下线、Qdrant 删除失败 | 用户立即不可见，清理任务继续重试 |
| reconcile 中断 | 从持久游标或幂等扫描继续 |

## 6. 验证计划

- 临时 application 测试覆盖公开状态机、边界、错误和取消，外部依赖全部使用 Mock。
- 临时 PostgreSQL 集成测试覆盖登记事务、并发领取、租约接管、owner 条件更新、重试、dead 和 outbox 幂等。
- 临时解析测试使用最小安全 fixture 覆盖四种 MIME、空/畸形/超限文件，验证后删除 fixture。
- 临时 Qdrant 集成测试覆盖重复 upsert、generation payload 过滤、删除、vector config/payload index 错误和 reconcile。
- 以故障注入运行上表每个关键点，并记录最终 PostgreSQL/Qdrant 状态。
- 核心新增公开逻辑覆盖率目标 80% 以上；记录后删除临时测试源码。
- 运行全量后端测试、vet、build，前端 lint/build，以及 worker/Compose smoke。

## 7. 阶段退出条件

### 7.1 P2-A 暂停门

- 管理员模型列表、测试、激活、版本历史和运行时解析均通过权限、边界、错误及脱敏验证。
- 管理员已在管理端完成至少一次真实测试和激活；证据只记录模型标识、revision、dimension、metric、时间和结果，不记录凭据。
- P2-A 证据及 9 份阶段文档已同步；随后状态切换为暂停，等待 D-002 决策、Qdrant 恢复和项目负责人明确继续。
- P2-A 完成不得被解释为 P2-B、P3 或其他后续阶段的启动授权。

### 7.2 P2-B / M2 退出条件

- 支持的四类文档均能从上传进入 active generation，并可追溯到资源、版本、资产、页码和 chunk。
- 重复投递与重启不会产生重复向量，所有故障注入最终收敛。
- 未发布、下线、删除和无权限资源不会因向量残留而可见。
- dead、backlog、处理耗时和 reconcile 差异有指标与告警入口。
- 资源页面能正确展示处理中、可重试失败和已发布状态，不暴露内部错误。
- 重建与清理命令有运行手册、dry-run/范围保护和恢复步骤。

## 8. 完成记录

| 字段 | 内容 |
|---|---|
| 状态 | `IN_PROGRESS`（P2-A/M2-A 已完成；P2 整体强制暂停，P2-B 未启动） |
| 负责人 | Codex（等待 D-002 决策、Qdrant 恢复及项目负责人解除暂停） |
| 开始日期 | 2026-09-01 |
| 完成日期 | P2-A：2026-09-04；P2：未完成 |
| 验证命令 | 隔离 PostgreSQL 迁移首次/重复执行；临时 application、repository、Vitest 验证；`go test ./... -count=1`、`go vet ./...`、`go build ./...`；`npm test -- --run --passWithNoTests`、`npm run lint`、`npm run build`；Playwright 桌面/移动端 Mock 流程；`git diff --check` |
| 验证结果 | `0018` 首次应用并复跑无待应用版本；管理员权限、SSRF/脱敏、原子激活、并发、来源漂移失败关闭及 revision 来源绑定通过；多 Key 全量验证、瞬时错误有限重试和维度漂移拒绝通过临时 Mock 验证。真实激活 `voyage-4-large` 的系统版本 `auto-v2-e5ec9a9f2abaa010`（1024 维、Cosine、`send_dimensions=false`、32/30/3）。通用探针携带可选 `encoding_format` 时上游 HTTP 400，省略后按完整契约真实复测成功；仅提交 `model_id` 的最小流程也成功自动识别 1024 维、生成内部 revision并完成激活，最终浏览器探测约 1.00 秒。UI 已收敛为仅模型必选、自动识别维度、revision 内部生成与高级参数折叠。P2-B 未启动。 |
| 覆盖率 | 临时后端核心路径覆盖 83.3% 至 100%，其中多 Key/重试相关函数覆盖 87.5% 至 100%；临时前端 `EmbeddingConfigPanel.tsx` statements 86.04%、functions 90.47%、lines 87.24%；临时测试源码和报告均已删除。 |
| 交付物 | P2-A：管理员模型测试/激活、不可变版本和运行时解析；P2-B：异步 API、worker、解析/切块/embedding/vector adapters、状态机、reconcile、前端状态 |
| 回滚或降级验证 | 无 active、来源停用或配置漂移时运行时失败关闭；激活在单事务中退役旧版本并启用新版本。 |
| 遗留风险 | D-002 的费用与数据合规尚未确认，Qdrant 当前不可用；D-002 决策和项目负责人明确继续前，P2-B 保持未启动。 |
