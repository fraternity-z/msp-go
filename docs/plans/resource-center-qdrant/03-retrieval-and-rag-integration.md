# P3 检索与 RAG 集成

> 状态：`IN_PROGRESS`（13/13 开发项已实现，M3 业务验收尚未通过）
> 里程碑：M3 MVP 可用
> 完整验收前置依赖：[P2-B 入库与向量索引](02-ingestion-and-vector-indexing.md) `DONE`；当前 P2-B 尚未完成，M3 不得提前通过
> 后续阶段：[P4 生产就绪](04-production-readiness.md)
> 模型配置：查询 embedding/rerank 只使用管理员在管理端激活的模型版本；代码、环境变量和请求参数不得另选模型。
> 当前执行：2026-09-05 按“推进至 P3 全部完成”补齐所有开发项，并使用真实隔离 PostgreSQL/Qdrant、外部模型 Mock 和浏览器验证。P2-B 尚未生产业务索引，D-004/D-005 尚无代表性语料与批准阈值，因此任务完成率不代替 M3 验收。

## 1. 阶段目标

完成 PostgreSQL 全文检索与 Qdrant 向量检索的并行召回、RRF 融合、可选 rerank、PostgreSQL 最终鉴权、上下文预算、引用生成和 Session 注入，形成可降级且无越权泄露的 MVP。

目标流程：

```text
查询 -> 查询分析/embedding
-> PostgreSQL 粗权限候选
-> PostgreSQL FTS || Qdrant vector search
-> RRF -> 可选 rerank
-> PostgreSQL 最终鉴权与状态复核
-> 邻接 chunk/上下文预算 -> 引用
-> KnowledgeRetriever -> Session ChatAgent
```

## 2. 预计影响范围

- `backend/internal/application/resource/`：检索用例、融合、引用和授权协作。
- `backend/internal/adapter/postgres/`：FTS、粗授权候选、最终授权与 chunk 元数据读取。
- `backend/internal/adapter/qdrant/`：Query/points search 和最小 payload filter。
- embedding/rerank adapter：只读取管理员激活配置，生成查询向量和执行可选重排。
- `backend/internal/application/session/`：窄 `KnowledgeRetriever` port、上下文预算和引用传递。
- `backend/internal/adapter/llm/einoagent/`：只消费已授权、已预算的知识上下文。
- `backend/cmd/api/`：装配、超时、降级和指标。
- 资源/会话前端：搜索、引用、降级和无结果状态。

## 3. 工作清单

- [x] **P3-01 检索请求契约**：定义 query、knowledge base、过滤条件、TopK、超时、trace ID、降级标志和稳定错误响应。
- [x] **P3-02 查询分析与 embedding**：规范化查询，管理员 active 不可变版本与当前 generation 契约匹配，可取消、有限重试；Voyage 使用 query input type，异常向量/未配置失败后 FTS 降级。
- [x] **P3-03 PostgreSQL 粗授权**：共享当前账户、默认租户/知识库、资源状态与 ACL 条件；向量粗资源范围最多 1000 项，超限显式降级而非静默截断。
- [x] **P3-04 Qdrant 向量召回**：tenant/knowledge-base/resource/generation-id/visibility 五个已索引 filter；只读九个身份 payload 字段、禁向量回传，point ID 经 PostgreSQL manifest 解析并核对完整身份。
- [x] **P3-05 PostgreSQL FTS 召回**：标题/正文加权 `simple` FTS，汉字查询补转义子串匹配；迁移 0019 增加正文 FTS 与标题/正文 trigram 索引，已做一万 chunk 开发容量验证，不宣称中文语义分词。
- [x] **P3-06 RRF 融合**：实现确定性 reciprocal rank fusion、去重和稳定 tie-breaker，记录各召回来源和原始排名。
- [x] **P3-07 可选 rerank**：管理员 `resource_reranker` 配置控制；最多 40 项/64 KiB、2 秒且服从剩余预算，未启用正常跳过，超时或非完整合法排列回退 RRF。
- [x] **P3-08 PostgreSQL 最终鉴权**：在返回正文前批量复核资源、版本、generation、发布/删除状态和 ACL；拒绝项不进入结果或日志正文。后续 Session 只可消费该边界的已授权结果。
- [x] **P3-09 邻接与上下文预算**：每个主命中最多补两个同版本父块/前后块，最终复验后独立返回；Session 共享原 16 KiB 动态预算，知识最多 8 KiB，整块序列化计入预算，当前问题不被截断。
- [x] **P3-10 引用与追溯**：稳定引用包含 knowledge-base/resource/version/chunk/page/section/title/hash；独立 citation GET 再次鉴权，撤权/失效统一 404，前端按页/章节展示，不读取旧缓存正文。
- [x] **P3-11 KnowledgeRetriever port**：Session 窄 Search 接口注入；首次/续聊/流式/历史共用知识元数据，Eino 仅接收授权且预算后的不可信资料，无 Qdrant client 耦合。
- [x] **P3-12 降级与错误契约**：FTS-only、vector-only、RRF 回退、none、预算不足、取消和最终鉴权 fail-closed 已验证；普通聊天可继续，不伪造引用。
- [x] **P3-13 指标、审计和前端状态**：固定低基数阶段/模式/原因的指标与无正文结构化日志；资源搜索与聊天展示加载、无结果、错误、普通回答、知识增强、降级及失效引用。

## 4. 权限不变量

1. Qdrant payload filter 只能减少候选，不能承担最终授权。
2. PostgreSQL 最终鉴权发生在 rerank 之后、构造 prompt 之前；任何缓存命中也必须满足同一规则。
3. 资源发布状态、active version/generation、ACL 或用户状态在检索期间变化时，以最终鉴权时的 PostgreSQL 结果为准。
4. 未授权 chunk 的正文、标题、摘要、分数和存在性不得出现在响应或模型上下文。
5. 引用打开时再次执行当前授权检查，不把旧检索结果当作长期访问凭证。
6. 默认租户/知识库只用于 MVP 兼容，不能绕过 owner 和显式 ACL。
7. 调用方不得指定 embedding/rerank 模型；无管理员 active 配置时按 D-008 降级，不使用代码或环境变量默认模型。

## 5. 质量与降级矩阵

| 场景 | 预期结果 |
|---|---|
| FTS 与向量均可用 | RRF 融合，可选 rerank，最终鉴权后返回 |
| Qdrant 超时/不可用 | FTS-only，返回降级标志并记录指标 |
| Query embedding 失败 | 不调用 Qdrant，按 D-008 使用 FTS-only 或明确失败 |
| Rerank 超时/无效输出 | 使用 RRF 结果，不暴露 provider 错误细节 |
| PostgreSQL 最终鉴权失败 | fail-closed，不返回候选正文 |
| 无授权候选 | 返回空知识结果，Session 可选择普通回答或拒答 |
| 引用资源随后下线 | 打开引用时拒绝，旧回答保留纯文本引用信息 |

## 6. 验证计划

- 临时 application 测试覆盖 RRF、去重、tie-breaker、TopK、预算、引用和错误路径，外部检索/模型使用 Mock。
- 临时 PostgreSQL 测试覆盖 FTS、批量最终鉴权、发布/删除竞态、owner/ACL 组合和空候选。
- 临时 Qdrant 测试覆盖 active generation payload filter、TopK、超时、point ID 和维度错误。
- 权限矩阵必须包含允许、拒绝、下线、删除、错误租户/知识库、缓存命中和状态竞态。
- 固定评测集记录 Recall@K、MRR/nDCG、引用正确率、无答案行为和相对 P0 基线。
- 目标容量档记录端到端 P50/P95/P99、各阶段耗时和降级率。
- 运行后端全量测试/vet/build、前端 lint/build，并以真实浏览器执行搜索、引用和 Session smoke。
- 记录覆盖率后删除临时测试源码和 fixture。

## 7. 阶段退出条件

- 固定评测集达到 D-004 的 MVP 阈值，结果可追溯到具体版本和参数。
- 代表性容量档达到 D-005 的 MVP 延迟要求，或有已批准的限制和后续计划。
- 权限矩阵无泄露，PostgreSQL 最终鉴权覆盖普通、降级和缓存路径。
- Session 可通过 `KnowledgeRetriever` 获得上下文与引用，Qdrant client 未进入 application/LLM adapter。
- Qdrant、embedding、rerank 故障均符合 D-008，FTS 降级可用。
- 前端能正确展示引用、无结果、处理中资源和降级状态。

## 8. 完成记录

| 字段 | 内容 |
|---|---|
| 状态 | `IN_PROGRESS`，13/13 开发项已实现；P2-B 与代表性质量/性能验收未满足，M3 未通过 |
| 负责人 | Codex |
| 开始日期 | 2026-09-05 |
| 完成日期 | 开发项 2026-09-06；阶段验收日期未定 |
| 验证命令 | 临时 application/HTTP/模型 Mock 及覆盖率；隔离 PostgreSQL 18 全部 19 个 migration；`P3_QDRANT_LIVE=1` 下全仓 `go test -race ./... -count=1`；`go vet ./...`、`go build ./...`；前端 `npm test -- --run --passWithNoTests`、`npm run lint`、`npm run build`；真实浏览器、`git diff --check` |
| 验证结果 | 全部通过，详细范围见 8.2。复核发现并修正邻接补全耗尽总超时和0页引用丢失问题；对应范围回归通过。无业务数据库变更、无外部模型调用。前端存在既有 Browserslist 数据与 chunk 大小提示，构建成功 |
| 覆盖率 | 新增 Search/GetCitation/refineSearch、HTTP 搜索/引用和指标 100%；其余新增核心受测函数至少83.3%，详见8.2；不代表全仓库覆盖率 |
| 交付物 | 混合检索与重排、PostgreSQL 两次授权及邻接、citation GET、Session/Eino 注入与知识元数据持久化、前端检索/引用状态、指标审计、0019 迁移及部署说明 |
| 回滚或降级验证 | 向量关闭/超时 FTS-only，重排失败 RRF 回退，全部失败无知识增强；授权失败无正文；旧引用重新鉴权，模型退役保留 FTS。0019 是增量 nullable 列与索引，回滚应用保留 schema 和历史元数据 |
| 遗留风险 | P2-B 入库生产链路未实现；当前模型真实检索质量、代表性语料/容量和 D-004/D-005 阈值未验收。合成向量与 Mock 模型只证明协议/过滤/组合链路，不证明真实语义质量 |

### 8.1 首轮只读检索切片（历史记录）

以下描述首轮 5/13 时的边界，当前实现以 8.2 和上方工作清单为准。

- `POST /api/v1/resources/search` 接受 `query`、`knowledge_base_id`、`top_k`、`timeout_ms`、`filters.type/chapter/topic`；身份由当前认证 principal 提供，租户只使用服务端默认值。JSON 最大 16 KiB，拒绝未知字段和尾随 JSON，不能注入用户、租户、模型或向量。
- query 最多 2000 个 Unicode 字符并规范空白；TopK 默认 5、范围 1-20；总超时默认 3000 ms、范围 100-10000 ms。每路召回与最终候选最多 100 项，保留剩余期限的三分之一用于最终授权；TopK 在最终授权之后应用。
- PostgreSQL 的标题高权重、chunk 正文次权重使用 `simple` FTS；过滤与最终加载共享相同的当前账户、默认租户、知识库 ACL、owner/旧 content ACL、发布/删除、版本、generation 和 manifest 条件。deny 优先，不支持部门归属时遇到生效的 department deny 保守拒绝。
- FTS 依赖知识库当前 generation 的不可变模型契约，不要求该模型仍为管理员 active；模型退役后已发布文本仍可用于词法降级。实际向量请求必须在后续 adapter 中重新解析管理员 active 版本和粗授权资源集合，不能把 `SearchScope` 单独当作向量授权过滤器。
- RRF 固定 `k=60`，每路重复候选只计一次，保留内部原始排名，按稳定标识破同分；不同 provider 原始分数不参与融合。响应正文只来自最终授权加载，并匹配 resource/version/chunk/generation 完整身份。
- 当前 API 装配的 vector retriever 为 `nil`：已有可检索数据时返回 `fts_only`、`degraded=true`、`vector_unavailable`；无当前索引或无可见数据时返回空 items，不伪造引用。内部召回全部失败返回 `none` 和明确降级原因，scope/最终授权查询失败返回脱敏 503，取消/总超时单独处理。
- 返回结构化 citation，但尚无引用打开 URL。16 KiB 只限制结果正文与展示文本，不是完整 JSON 或 Session prompt 预算；整块超预算时跳过，不截断内容和引用。旧资源详情接口不作为新的受授权引用入口。
- 本轮未增加 FTS 持久索引，`simple` 不提供中文分词或任意子串匹配；需在后续中文召回和容量验证后补索引/分词策略。端点不会自动解析旧资源或回填 chunk。

### 8.2 完整开发项与隔离验证

- 装配：`QDRANT_ENABLED` 控制 vector adapter；embedding 只读取管理员 active 契约；rerank 复用管理员 `resource_reranker` Agent 配置，不允许普通检索请求选模型。重排前先授权正文，重排及邻接后重新加载当前授权正文；manifest ID 是 Qdrant point ID。
- 邻接补全独立限制为最多 500 ms 且不超过剩余请求期限的一半，超时回退主命中并保留最终鉴权时间；父请求取消仍立即传播。引用页码保留数据库原值，0/null 不展示页码也不推断偏移，章节与引用仍有效。
- 引用：`GET /api/v1/resources/citations/{chunk_id}` 绑定知识库、version、generation，返回当前授权 `SearchHit`。所有搜索/引用响应 `Cache-Control: no-store`，撤权/下线/删除/旧代际统一拒绝。没有检索缓存或旧详情绕行路径。
- Session：知识独立作为不可信用户资料消息，固定 Tutor 指令保留高优先级；16 KiB 是动态输入预算，包含模式、问题、附件占用、历史与知识序列化，不能解释为完整 wire prompt 上限。知识最多 8 KiB，引用只属于实际注入的块。普通/计量/首聊消息均只存知识元数据；历史知识助手回复不再次进入模型上下文。
- 指标：`msp_resource_search_*` 记录请求结果、total/scope/fts/vector/authorize/rerank/neighbors 耗时、两路候选、两次鉴权过滤、引用和空结果量、固定降级原因；不记录 query、来源正文、用户/资源 ID 或 provider 原始错误。
- 真实 PostgreSQL 18：19 个迁移首次与重复执行、23 个 ACL 组合、manifest 身份、父块/前后块、引用撤权/删除/版本/generation 变化、中文与通配符转义、重排期间撤权、1001 资源限流及数据库错误均通过。Session 五个持久化场景通过。
- 真实 Qdrant 1.14.1：Docker 既有 socket 故障下使用官方 Windows 原生隔离实例。Qdrant -> VectorRetriever -> PostgreSQL manifest/最终授权 -> HTTP 已通过：合法点返回，伪 chunk、无权限 resource 和无 manifest 的点拒绝，撤权和删除后不返回正文；模型为 Mock。
- 合成开发基线：总计 10000 chunks，并发 5、每模式 25 次 HTTP 请求；FTS P50/P95/P99 为 315/409/413 ms，Mock hybrid 为 310/334/339 ms，均零失败。10 个固定中文数学术语、每题一个已知相关块的 Recall@5/MRR/nDCG@5 均为 1；每模式 33 条主/邻接引用身份及 hash 正确率 100%。该样本是精确术语与确定性 Mock，不是代表性语义评测，不替代 D-004/D-005 或生产 SLO。
- 前端：24 项临时 Mock 测试；真实浏览器覆盖正常、降级、空结果、错误、取消、引用 404、历史与流式引用以及页码 0 的兼容。320/390/1440 像素截图与实际元素边界通过；修复手机侧栏遮挡及窄屏筛选文本截断。
- 新增核心覆盖：Search/GetCitation/refineSearch/metrics 100%，vector 核心 100%，模型 adapter 98.7%，PostgreSQL 新函数 83.9%-97.1%，Session 核心 83.3%-100%，前端新实现语句/行/函数 100%、分支 98.16%。这些是本次受测实现的覆盖，不是全仓库覆盖率。
- 临时测试源码与专用 fixture 按仓库规则在最终全量验证后删除；持久交付只包含生产实现、迁移与文档，不改变业务数据库或管理员凭据。

### 8.3 提交前开发交付验收（2026-09-06）

- 结论：P3 的 13 项开发交付通过提交前验收；独立复核检索权限、向量身份、模型配置、Session/Eino、持久化、迁移和前端状态，未发现阻止提交的问题。
- 本次重新执行 `go vet ./...`、`go build ./...`、前端 `npm run lint`、`npm run build`、Go 格式及 `git diff --check`，全部通过；前端保留既有 Browserslist 数据过期和 bundle 大小提示。
- 8.2 的真实 PostgreSQL/Qdrant 集成、race、覆盖率、24 项前端测试和浏览器结果属于此前已执行证据，本次核对其与交付代码一致。临时测试已按仓库规则删除，本次未将空测试运行算作功能复测，浏览器接口 Mock 不代替真实业务入库链路。
- 本次验收允许提交开发成果，不改变 P2-B、D-004/D-005 和 M3 未通过的状态；未执行业务数据库迁移、外部模型调用或远程发布。新 API 启动前仍须先应用 0019。
