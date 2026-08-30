# P4 生产就绪

> 状态：`TODO`
> 里程碑：M4 生产就绪
> 前置依赖：[P3 检索与 RAG 集成](03-retrieval-and-rag-integration.md) `DONE`
> 后续阶段：[P5 性能与质量](05-performance-and-quality.md)

## 1. 阶段目标

把 MVP 从单机开发能力提升为可安全部署、可观测、可恢复、可升级和可值守的生产系统。生产环境使用 Milvus Distributed 或经批准的托管服务，不使用开发 Standalone 作为生产拓扑。

## 2. 预计影响范围

- 生产 Compose/编排、网络、TLS、secret、账号和资源配额配置。
- `backend/cmd/vector-worker` 的多实例、优雅停止、健康与 readiness。
- metrics、tracing、structured logging、audit 和告警。
- PostgreSQL、对象存储、Milvus collection/generation 的备份、恢复与重建工具。
- 发布、升级、蓝绿 generation 切换、回滚和事故运行手册。
- 管理端处理状态、dead job、backlog、reconcile 和重建可见性。

## 3. 工作清单

- [ ] **P4-01 生产拓扑落地**：按 D-010 部署 Distributed/托管 Milvus，定义依赖版本、资源、节点、存储和可用区边界。
- [ ] **P4-02 网络与身份安全**：配置私网、TLS、服务账号、最小权限、secret 注入、证书轮换和访问审计，禁止默认账号/明文凭据。
- [ ] **P4-03 Worker 高可用**：验证多实例 claim/lease、并发上限、优雅停止、滚动升级、backpressure 和异常接管。
- [ ] **P4-04 指标与 tracing**：覆盖 API、outbox、job、parser、embedding、Milvus、FTS、rerank、final auth、reconcile 和 generation 切换。
- [ ] **P4-05 安全审计日志**：记录操作者、资源、版本、动作、结果和 trace；正文、向量、密钥、Token 与敏感 ACL 不入日志。
- [ ] **P4-06 Dashboard 与告警**：建立延迟、错误率、backlog age、dead、租约超时、向量漂移、降级率、质量探针和容量告警。
- [ ] **P4-07 备份策略**：按 D-009 落地 PostgreSQL custom archive、对象存储版本/备份、Milvus 元数据与必要快照；明确向量可重建边界。
- [ ] **P4-08 恢复与重建演练**：从 PostgreSQL 和对象存储重建 collection，验证 checksum、数量、generation、权限和 RPO/RTO。
- [ ] **P4-09 升级与蓝绿切换**：验证 schema/SDK/服务升级、双 generation 构建、质量门禁、原子切换和旧 generation 保留/清理。
- [ ] **P4-10 降级与故障演练**：分别中断 Milvus、embedding、rerank、对象存储、PostgreSQL 和 worker，验证 D-008、告警和恢复。
- [ ] **P4-11 运行手册**：形成 backlog、dead job、lease、reconcile、重建、证书/密钥轮换、容量和事故响应手册。
- [ ] **P4-12 生产验收**：在准生产完成安全扫描、备份恢复、滚动升级、故障演练、容量 smoke 和值班交接。

## 4. 生产安全门禁

1. 生产 Milvus 不暴露公网管理端口，所有链路使用已批准的 TLS 和服务身份。
2. 密钥只从受控 secret 来源注入，不写入 `.env.example`、日志、错误、任务 payload 或备份说明。
3. 文档解析运行在有资源限制的隔离边界，防止恶意文档消耗 CPU、内存、磁盘或触发外部访问。
4. PostgreSQL 最终鉴权不可因缓存、降级或 Milvus 故障被绕过。
5. 备份加密、访问、保留和销毁符合 D-009；恢复环境同样受权限控制。
6. 观测 label 不包含用户 ID、资源原文、原始 query 或错误全文等高基数/敏感数据。

## 5. 关键演练

| 演练 | 通过条件 |
|---|---|
| Worker 滚动重启 | 无 job 丢失；租约接管后最终收敛；无重复向量 |
| Milvus 故障 | API 按 D-008 降级，告警及时，恢复后 reconcile 清零差异 |
| Embedding provider 故障 | 队列受控增长，无无限重试或敏感错误泄露 |
| PostgreSQL 恢复 | 恢复业务真相、任务和 active generation，应用按顺序恢复 |
| Collection 丢失 | 从 PostgreSQL+对象存储重建并在 RTO 内切换 |
| 错误 generation 发布 | 质量门禁阻止切换，或可原子切回旧 generation |
| 凭据轮换 | 新旧凭据有受控窗口，业务不中断，旧凭据按期失效 |

## 6. 验证计划

- 临时多实例测试覆盖租约接管、滚动停止和并发 worker；结束后删除测试源码。
- 在准生产执行备份、恢复、collection 重建和 generation 回滚，记录实际 RPO/RTO。
- 使用故障注入逐项验证依赖中断、告警和降级；不能只以单元 Mock 代替。
- 运行漏洞扫描、依赖审计、配置扫描和敏感信息扫描。
- 执行全量测试、race、vet、build、前端 lint/build，以及 API/worker/浏览器 smoke。
- 验证 dashboard 指标与实际故障一致，无高基数或敏感 label。

## 7. 阶段退出条件

- 生产拓扑、TLS、服务账号和网络隔离通过安全评审。
- Worker 重启无丢任务，dead/backlog/reconcile 有可执行运维闭环。
- PostgreSQL、对象存储和 Milvus 恢复/重建达到 D-009 的 RPO/RTO。
- 全部关键故障演练达到 D-008，告警和运行手册可由非开发人员执行。
- generation 蓝绿发布与回滚完成一次实操。
- 准生产容量 smoke、依赖审计和敏感信息检查通过。

## 8. 完成记录

| 字段 | 内容 |
|---|---|
| 状态 | `TODO` |
| 负责人 | 待定 |
| 开始日期 |  |
| 完成日期 |  |
| 验证命令/演练 |  |
| 验证结果 |  |
| 覆盖率 |  |
| 交付物 | 生产拓扑、观测、告警、备份恢复、升级回滚、运行手册 |
| 回滚或降级验证 |  |
| 遗留风险 |  |

