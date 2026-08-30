# P3 检索与 RAG 集成

> 状态：`TODO`
> 里程碑：M3 MVP 可用
> 前置依赖：[P2 入库与向量索引](02-ingestion-and-vector-indexing.md) `DONE`
> 后续阶段：[P4 生产就绪](04-production-readiness.md)

## 1. 阶段目标

完成 PostgreSQL 全文检索与 Milvus 向量检索的并行召回、RRF 融合、可选 rerank、PostgreSQL 最终鉴权、上下文预算、引用生成和 Session 注入，形成可降级且无越权泄露的 MVP。

目标流程：

```text
查询 -> 查询分析/embedding
-> PostgreSQL 粗权限候选
-> PostgreSQL FTS || Milvus vector search
-> RRF -> 可选 rerank
-> PostgreSQL 最终鉴权与状态复核
-> 邻接 chunk/上下文预算 -> 引用
-> KnowledgeRetriever -> Session ChatAgent
```

## 2. 预计影响范围

- `backend/internal/application/resource/`：检索用例、融合、引用和授权协作。
- `backend/internal/adapter/postgres/`：FTS、粗授权候选、最终授权与 chunk 元数据读取。
- `backend/internal/adapter/milvus/`：向量查询和最小 scalar filter。
- embedding/rerank adapter：查询向量和可选重排。
- `backend/internal/application/session/`：窄 `KnowledgeRetriever` port、上下文预算和引用传递。
- `backend/internal/adapter/llm/einoagent/`：只消费已授权、已预算的知识上下文。
- `backend/cmd/api/`：装配、超时、降级和指标。
- 资源/会话前端：搜索、引用、降级和无结果状态。

## 3. 工作清单

- [ ] **P3-01 检索请求契约**：定义 query、knowledge base、过滤条件、TopK、超时、trace ID、降级标志和稳定错误响应。
- [ ] **P3-02 查询分析与 embedding**：规范化查询，执行可取消的 query embedding；空、超长、模型失败和维度不符有明确行为。
- [ ] **P3-03 PostgreSQL 粗授权**：基于当前用户、默认租户/知识库、资源状态和 ACL 生成有界候选或 filter，不把它视为最终授权。
- [ ] **P3-04 Milvus 向量召回**：按 active generation、模型契约和最小过滤字段搜索，限制 TopK、输出字段和超时。
- [ ] **P3-05 PostgreSQL FTS 召回**：对标题、正文/chunk 文本和必要元数据建立可解释搜索，使用与向量召回一致的资源范围。
- [ ] **P3-06 RRF 融合**：实现确定性 reciprocal rank fusion、去重和稳定 tie-breaker，记录各召回来源和原始排名。
- [ ] **P3-07 可选 rerank**：按 D-008 设置开关、超时、批量和回退；失败不得让已授权基础召回不可用。
- [ ] **P3-08 PostgreSQL 最终鉴权**：在生成上下文前批量复核资源、版本、generation、发布/删除状态和 ACL；拒绝项不进入结果、日志正文或模型 prompt。
- [ ] **P3-09 邻接与上下文预算**：按 chunk 邻接关系补上下文，复用并调整 Session 当前预算，避免截断引用或挤压用户问题。
- [ ] **P3-10 引用与追溯**：返回 resource/version/chunk/page/section/title 等稳定引用，前端可打开仍获授权的资源位置。
- [ ] **P3-11 KnowledgeRetriever port**：在 Session application 注入窄接口；Session、Eino adapter 和 HTTP 层均不直接依赖 Milvus。
- [ ] **P3-12 降级与错误契约**：验证向量不可用时 FTS-only、rerank 不可用时 fused-only、全部不可用时明确无知识增强，不伪造引用。
- [ ] **P3-13 指标、审计和前端状态**：记录各阶段耗时、候选量、过滤量、降级原因、引用数和无结果率；UI 区分普通回答、知识增强和降级。

## 4. 权限不变量

1. Milvus filter 只能减少候选，不能承担最终授权。
2. PostgreSQL 最终鉴权发生在 rerank 之后、构造 prompt 之前；任何缓存命中也必须满足同一规则。
3. 资源发布状态、active version/generation、ACL 或用户状态在检索期间变化时，以最终鉴权时的 PostgreSQL 结果为准。
4. 未授权 chunk 的正文、标题、摘要、分数和存在性不得出现在响应或模型上下文。
5. 引用打开时再次执行当前授权检查，不把旧检索结果当作长期访问凭证。
6. 默认租户/知识库只用于 MVP 兼容，不能绕过 owner 和显式 ACL。

## 5. 质量与降级矩阵

| 场景 | 预期结果 |
|---|---|
| FTS 与向量均可用 | RRF 融合，可选 rerank，最终鉴权后返回 |
| Milvus 超时/不可用 | FTS-only，返回降级标志并记录指标 |
| Query embedding 失败 | 不调用 Milvus，按 D-008 使用 FTS-only 或明确失败 |
| Rerank 超时/无效输出 | 使用 RRF 结果，不暴露 provider 错误细节 |
| PostgreSQL 最终鉴权失败 | fail-closed，不返回候选正文 |
| 无授权候选 | 返回空知识结果，Session 可选择普通回答或拒答 |
| 引用资源随后下线 | 打开引用时拒绝，旧回答保留纯文本引用信息 |

## 6. 验证计划

- 临时 application 测试覆盖 RRF、去重、tie-breaker、TopK、预算、引用和错误路径，外部检索/模型使用 Mock。
- 临时 PostgreSQL 测试覆盖 FTS、批量最终鉴权、发布/删除竞态、owner/ACL 组合和空候选。
- 临时 Milvus 测试覆盖 active generation、filter、TopK、超时和维度错误。
- 权限矩阵必须包含允许、拒绝、下线、删除、错误租户/知识库、缓存命中和状态竞态。
- 固定评测集记录 Recall@K、MRR/nDCG、引用正确率、无答案行为和相对 P0 基线。
- 目标容量档记录端到端 P50/P95/P99、各阶段耗时和降级率。
- 运行后端全量测试/vet/build、前端 lint/build，并以真实浏览器执行搜索、引用和 Session smoke。
- 记录覆盖率后删除临时测试源码和 fixture。

## 7. 阶段退出条件

- 固定评测集达到 D-004 的 MVP 阈值，结果可追溯到具体版本和参数。
- 代表性容量档达到 D-005 的 MVP 延迟要求，或有已批准的限制和后续计划。
- 权限矩阵无泄露，PostgreSQL 最终鉴权覆盖普通、降级和缓存路径。
- Session 可通过 `KnowledgeRetriever` 获得上下文与引用，Milvus SDK 未进入 application/LLM adapter。
- Milvus、embedding、rerank 故障均符合 D-008，FTS 降级可用。
- 前端能正确展示引用、无结果、处理中资源和降级状态。

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
| 交付物 | 混合检索、最终鉴权、引用、KnowledgeRetriever、Session/前端集成、指标 |
| 回滚或降级验证 |  |
| 遗留风险 |  |

