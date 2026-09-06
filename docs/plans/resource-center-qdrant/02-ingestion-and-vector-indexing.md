# P2 入库与向量索引

> 状态：`DONE`
> 里程碑：M2-A 管理员模型配置就绪 / M2-B 入库索引闭环
> 前置依赖：[P1 数据与契约基础](01-data-and-contract-foundation.md) `DONE`
> 后续阶段：[P3 检索与 RAG 集成](03-retrieval-and-rag-integration.md)
> 当前子阶段：P2-A/M2-A 与 P2-B/M2-B 均完成；2026-09-06 已获继续开发及测试环境真实评测授权。
> 模型配置：Embedding 模型由管理员在管理端测试并激活，运行时只读取唯一 active 的不可变版本，不允许代码、环境变量或普通请求方覆盖；具体评测模型契约以验收记录为准。
> 验收记录：[P0-P3 测试环境验收](TEST-ACCEPTANCE-2026-09-06.md)。本阶段完成不代替 P3 的质量与容量验收，也不表示已部署到生产。

## 1. 阶段目标

P2-A 建立管理员控制的 embedding 模型配置、验证、激活和运行时解析闭环；P2-B 实现资源上传到可检索向量的完整异步链路，并验证重复投递、租约失效、模型失败、删除、下线和重建最终收敛到 PostgreSQL 记录的状态。P2-A 后的历史暂停已于本轮明确继续授权后解除。

P2-B 目标流程：

```text
受控上传暂存 -> DRAFT -> resource_document + document_version + job + outbox
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

历史执行记录：P2-A 验证后曾暂停等待继续授权；2026-09-06 后续授权已覆盖以下 P2-B 实现和测试环境评测。

### 3.2 P2-B 入库索引闭环

- [x] **P2-B-01 异步 API 契约**：独立 multipart 入库接口返回 202 和资源/version/job 标识；owner 状态列表、详情、重试、下线与删除返回稳定状态、阶段、可操作性和脱敏错误码。
- [x] **P2-B-02 原子登记事务**：同一 PostgreSQL 事务登记内容、不可变源/版本、membership、job 和 outbox；owner/idempotency key 与内容及元数据摘要绑定。写对象前登记暂存，事务失败或提交结果未知均由受引用保护的清理收敛。
- [x] **P2-B-03 独立 worker 入口**：`backend/cmd/vector-worker` 已接入 Docker/Compose vector profile，支持信号、loopback 健康/指标、优雅停止和结构化日志；原生进程与管理接口已实测。
- [x] **P2-B-04 任务领取与租约**：`FOR UPDATE SKIP LOCKED`、owner/attempt fence、heartbeat、超时接管、最大重试次数及 1-8 并发均经真实 PG 和 Mock 回归。
- [x] **P2-B-05 受控对象读取**：只接收本地或私有存储中的服务端对象；同一存储快照绑定暂存与上传，读取复核 namespace、MIME、大小、checksum、超时和取消，拒绝任意 URL/路径。
- [x] **P2-B-06 文档解析**：PDF、DOCX、TXT、MD 均通过真实入库闭环；空文档、扫描/加密 PDF、畸形文件、页数/字符及压缩包限制由临时 fixture 验证并输出稳定错误码。
- [x] **P2-B-07 文本规范化**：统一换行、Unicode NFC、控制字符和空白；保留页码、章节、段落及内容摘要，原文不进入任务日志或向量 payload。
- [x] **P2-B-08 确定性切块**：稳定顺序、边界、重叠、父块/邻接及版本内 deterministic chunk ID；重放对已存在块进行一致性核验。
- [x] **P2-B-09 Embedding 批处理**：管理员 active 契约、批量字节/数量预算、限速、超时、取消、有限重试与返回维度/顺序校验已实现；真实原创语料完成文档 embedding。
- [x] **P2-B-10 Collection 与 generation 写入**：每代独立 collection，严格匹配维度、metric、model/revision 与 payload indexes，契约变化失败关闭。
- [x] **P2-B-11 幂等 upsert**：manifest 作为 deterministic point ID；确认写入后逐点核对身份和 hash payload，再核对数量，重复执行不增加 point。
- [x] **P2-B-12 发布、下线与删除**：完整 receipts 和当前模型复查通过才发布；撤销先更新 PG 再排 purge，旧 API 删除也进入同一流程。撤销阻塞文档后重新评估 building，避免其余成功文档停滞。
- [x] **P2-B-13 重试、dead 与背压**：有限退避/dead、上传前跨进程 1000 槽上限、固定低基数队列指标已实现；同模型重建重试仍指向失败的 building 代。30 天终态清理保留幂等身份及最后安全状态摘要。
- [x] **P2-B-14 Reconcile 与重建**：双阶段持久游标、有界分页、缺失/错误修复、多余点删除前实时 PG 复查；真实缺 1/错 1/多 1 演练恢复差异为 0，重建切换与旧代 7 天保留通过。

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

### 7.1 P2-A 历史暂停门

- 管理员模型列表、测试、激活、版本历史和运行时解析均通过权限、边界、错误及脱敏验证。
- 管理员已在管理端完成至少一次真实测试和激活；证据只记录模型标识、revision、dimension、metric、时间和结果，不记录凭据。
- P2-A 证据及阶段文档曾同步并暂停等待继续授权；本轮已获得 P2-B 开发与测试环境真实评测授权。
- 历史 P2-A 完成记录本身不是后续启动授权，当前执行依据为项目负责人后续明确指令。

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
| 状态 | `DONE`，P2-A 5/5、P2-B 14/14；M2 入库闭环验收完成 |
| 负责人 | Codex |
| 开始日期 | 2026-09-01 |
| 完成日期 | P2-A：2026-09-04；P2-B：2026-09-06 |
| 验证命令 | 隔离 PostgreSQL 迁移首次/重复执行；临时 application、repository、Vitest 验证；`go test ./... -count=1`、`go vet ./...`、`go build ./...`；`npm test -- --run --passWithNoTests`、`npm run lint`、`npm run build`；Playwright 桌面/移动端 Mock 流程；`git diff --check` |
| 验证结果 | P2-A 管理员激活、来源绑定及失败关闭通过；P2-B 真实 PG 21 个迁移、原子登记/并发/lease/receipts/重试/删除/重建/保留清理通过，四格式真实上传到发布检索引用通过。缺 1/错 1/多 1 演练排修复 2、清多余 1，恢复后差异 0；新代完成前旧代可查，切换后旧引用拒绝且旧代向量保留。最新详情见测试环境验收记录。 |
| 覆盖率 | P2-A 历史核心路径 83.3%-100%；本轮 resource_ingestion 仓储 686/760 语句=90.26%，history 91.4%、uploads 96.1%；worker CLI 88.9%，新增 maintenance 100%。临时测试/fixture 在统一验收后删除，不提交测试源码。 |
| 交付物 | P2-A：管理员模型测试/激活、不可变版本和运行时解析；P2-B：异步 API、worker、解析/切块/embedding/vector adapters、状态机、reconcile、前端状态 |
| 回滚或降级验证 | 无 active、来源停用/漂移失败关闭；切代前旧代持续可用，失败/dead 不误发布；撤销立即影响 PG 授权，向量清理失败可重试。0020/0021 为增量迁移，回退应用时保留身份/暂存/状态摘要，暂停 worker 后维护。 |
| 证据边界 | 原生 PostgreSQL/Qdrant 与 worker smoke、Compose 配置验证通过；本机 Docker Linux engine 管道缺失，实际镜像构建未能执行，未宣称容器运行通过。P3 容量与最终 M3 结论独立登记。 |

### 8.1 API 与状态

- `POST /api/v1/resources/ingestions`：multipart `file`、`title`、`chapter`、`topic` 与 `client_request_id`；最大 50 MiB，只允许 PDF/DOCX/TXT/MD，模型和 storage key 均由服务器决定。成功返回 202 的 `resource_id`、`document_version_id`、`job_id`。
- `GET /api/v1/resources/ingestions` 与 `GET /api/v1/resources/ingestions/{resource_id}`：仅当前教师/管理员所有者可读，提供分页与安全状态。
- `POST /api/v1/resources/ingestions/{resource_id}/retry`、`POST /api/v1/resources/ingestions/{resource_id}/unpublish`、`DELETE /api/v1/resources/ingestions/{resource_id}`：按状态开放操作，非 owner 不返回资源信息。
- `state` 为 queued/processing/published/failed/dead/unpublished/deleted，另有 publication_status、stage、retryable、can_retry/can_unpublish/can_delete；URI、provider 原始错误和凭据不返回。旧资源编辑允许等值不可变字段与标题/章节等元数据修改，实际更换源或正文返回冲突。
- 解析上限为 200 页、200 万字符；DOCX 解压 XML 和块数也有独立上限。扫描 PDF 需要 OCR 时给出明确失败码，本阶段不将空文本误发为已发布。

### 8.2 Worker 与维护

先应用 0020/0021，再启动 API/worker。worker 与 API 使用相同管理员配置解密密钥与上传目录，Compose 的 vector profile 共享 uploads 可写挂载；运行镜像安装 `poppler-utils` 和 CA 证书。`--pdfinfo`/`--pdftotext` 可指定受控可执行文件，`--concurrency` 默认 2、范围 1-8。

```text
msp-vector-worker run --listen=127.0.0.1:8091 --concurrency=2
msp-vector-worker reconcile --generation=<generation-uuid>
msp-vector-worker reconcile --generation=<generation-uuid> --apply
msp-vector-worker rebuild --knowledge-base=<knowledge-base-uuid>
msp-vector-worker rebuild --knowledge-base=<knowledge-base-uuid> --apply
```

- reconcile/rebuild 默认 dry-run，变更必须 `--apply` 且显式 generation/knowledge-base 范围。generation 只能来自 PG 登记，collection 名为 `resource_<kb-32hex>_<generation-32hex>`；不接收运维调用方的模型覆盖。
- 自动对账默认每 5 分钟，每轮最多 100 个 generation；每阶段 `--max-pages` 默认 200，JSON 游标持久化。`complete=false` 表示有界分页未完成，下次继续，不当作 provider 故障。
- `/health` 只反映 PG/Qdrant 依赖就绪，`/metrics` 只在 loopback 暴露；不等同外部 embedding 可用性测试。模型不可用通过任务错误码、重试与失败指标体现。停机取消子任务并等待 heartbeat/服务退出，当前 lease 可由后续 worker 接管。
- 每轮对账后执行独立、最多 1 分钟的清理：终态已超过 30 天的 jobs/outbox 各最多 1000 条。只移除 resource ingestion 记录；resource/document 幂等身份、最后一次任务安全摘要以及重试/发布检查仍保留。
- 上传写入前登记 staging，并在 PG 事务成功后清除已引用暂存。超过 24 小时且任一 document/version/旧 asset 均未引用的暂存才可领取删除，每轮最多 8 个，删除租约 15 分钟；claim 与登记互斥，删除成功后凭 token 完成，超时或 namespace 不匹配保留记录重试。
- 旧代向量保留 7 天；资源下线或删除后 PostgreSQL 立即阻断读取，并通过异步 purge 清理该资源的向量，旧代保留期不延迟资源撤销。重建失败先检查稳定错误码和管理员配置，修复后针对原 building 重试；不要直接修改 generation/model 身份。

### 8.3 指标与告警入口

- 队列：`msp_resource_ingestion_queue_jobs{state="queued|running|dead"}`、`msp_resource_ingestion_oldest_wait_seconds`；初始告警建议 dead > 0 持续 10 分钟、queued > 800 持续 10 分钟或最老等待 > 600 秒。
- 对账：`msp_resource_ingestion_reconcile_differences_total{kind="missing|mismatched|extra|scheduled|removed"}` 与 `msp_resource_ingestion_reconcile_runs_total{result="complete|partial|failed"}` 为累计观察次数，不表示去重后的未修复点数。15 分钟失败增加 3 次告警；连续多个完整周期仍观察到差异时排查修复队列。partial 持续增加且长时间无 complete 时检查容量、页数与游标进度。
- 清理：`msp_resource_ingestion_history_deleted_total{kind="jobs|outbox"}`、`msp_resource_ingestion_history_cleanup_failures_total`、`msp_resource_ingestion_uploads_removed_total`、`msp_resource_ingestion_upload_cleanup_failures_total`；连续 3 个周期失败时检查 PG、目录写权限或原存储 namespace。上述为初始运维阈值，实际告警系统按部署流量校准。
- 本轮原生 CLI 使用独立测试库运行了 14 次自动对账，health=ok，固定指标可读且清理失败为 0；dry-run 未排任务、未删点。真实模型质量、容量和完整故障演练结果统一引用[测试环境验收](TEST-ACCEPTANCE-2026-09-06.md)。
