# P6 多租户、高可用与智能增强

> 状态：`TODO`
> 里程碑：M6 高级能力就绪
> 前置依赖：[P5 性能与质量](05-performance-and-quality.md) `DONE`
> 模型配置：多语言、多模态及升级候选模型均由管理员在管理端测试并激活；自动化只能生成候选和证据，不能绕过管理员切换 active 版本。
> 执行约束：P2-A 完成后的暂停门必须先由管理员确认和项目负责人明确解除；否则本阶段不得启动。

## 1. 阶段目标

在 MVP 和生产运维基础稳定后，完成真实多租户组合权限、资源隔离与配额、跨可用区/跨区域容灾，以及多语言、父子检索、查询扩展、自适应 TopK、多模态和模型平滑升级。

P6 的高级能力不得削弱 P3 已证明的 PostgreSQL 最终鉴权和 P4 的恢复边界。

## 2. 工作清单

- [ ] **P6-01 租户领域模型**：建立 tenant、membership、department/role 等正式模型，迁移默认租户数据并定义生命周期。
- [ ] **P6-02 PostgreSQL RLS/授权**：在 application 授权之外按批准范围加入 RLS、约束和审计，明确服务账号与运维例外。
- [ ] **P6-03 组合 ACL**：实现用户、部门、角色、资源、知识库、allow/deny 优先级和继承，形成可解释授权结果。
- [ ] **P6-04 Tenant placement**：按规模/合规将租户映射到共享 collection + payload filter、custom shard key、独立 collection 或独立集群，支持受控迁移。
- [ ] **P6-05 配额与资源治理**：限制租户存储、文档、chunk、QPS、embedding/rerank 成本、并发和重建资源。
- [ ] **P6-06 多租户隔离验证**：覆盖 API、任务、Qdrant payload filter/shard key、cache、reconcile、日志、snapshot、恢复和运维工具的跨租户负向测试。
- [ ] **P6-07 跨可用区高可用**：验证 PostgreSQL、对象存储、Qdrant、worker 和 API 的故障转移、数据一致性与容量。
- [ ] **P6-08 跨区域容灾**：按 D-009 实现复制/备份、灾难恢复、DNS/流量切换、回切和定期演练。
- [ ] **P6-09 多语言检索**：引入语言识别、适配 embedding/分词和跨语言评测，保证引用原文可追溯。
- [ ] **P6-10 高级文本检索**：实现父子 chunk、query expansion、adaptive TopK 和邻接策略，并通过固定评测证明增益。
- [ ] **P6-11 多模态检索**：为图片、公式、表格或其他批准模态定义版本、embedding、索引、权限和引用契约。
- [ ] **P6-12 模型平滑升级**：支持 shadow、双写/双索引、离线评测、灰度、管理员激活、active generation 切换、回滚和旧代清理。

## 3. 多租户安全不变量

1. tenant ID 必须来自已认证主体或受控服务上下文，不接受用户用请求字段任意覆盖。
2. PostgreSQL 最终鉴权必须同时验证 tenant、knowledge base、resource/version 状态和组合 ACL。
3. Qdrant payload filter、custom shard key 或 collection 只提供逻辑、物理或性能隔离，不能替代最终授权。
4. cache、job、outbox、idempotency key、generation、指标和备份均包含明确 tenant 边界。
5. 运维跨租户操作必须显式、最小范围、可审计，并支持 dry-run 和二次确认。
6. 独立 collection/集群迁移期间不得产生双边均 active 的未授权窗口。
7. 自动评测或发布系统不得自行激活模型；active 模型变更必须由管理员发起、留存审计并满足 generation 回滚保护。

## 4. 高可用与模型升级演练

| 演练 | 通过条件 |
|---|---|
| 单可用区故障 | 服务在目标 RTO 内恢复，已确认写入满足 RPO，任务最终收敛 |
| 区域切换与回切 | 流量、业务真相、对象、active generation 和权限一致 |
| 租户 placement 迁移 | 迁移前后结果、权限、数量和引用一致，无跨租户可见性 |
| 新 embedding shadow | 不影响线上结果，差异和成本可观测 |
| 双索引灰度 | 质量/性能门禁通过后原子切换，可立即回滚 |
| 旧 generation 清理 | 保留窗口后只删除无引用代际，reconcile 无异常 |

## 5. 验证计划

- 建立包含多租户、部门、角色、allow/deny、停用用户和资源状态的组合权限矩阵。
- 使用临时测试覆盖所有公开授权函数、边界和错误；外部依赖使用 Mock，核心逻辑覆盖率 80% 以上。
- 在真实 PostgreSQL/Qdrant 隔离环境执行跨租户负向集成测试，验证 payload filter、shard key、cache、snapshot 和运维工具。
- 执行跨可用区/跨区域故障、恢复、回切和 tenant placement 迁移演练，记录实际 RPO/RTO。
- 为每项高级检索能力单独做 A/B 离线评测，只有质量增益超过成本/延迟门槛才启用。
- 模型升级按 shadow、双索引、灰度、切换、回滚、清理完整执行一次。
- 删除临时测试源码和 fixture，保留命令、结果、覆盖率和受控证据链接。

## 6. 阶段退出条件

- 组合权限与 RLS 策略通过全矩阵，无跨租户数据、向量、缓存或日志泄露。
- 共享 collection、custom shard key、独立 collection 和独立集群 placement 均有明确阈值与迁移工具。
- 跨可用区/区域演练达到 D-009 的 RPO/RTO，并完成回切。
- 多语言、父子、query expansion、自适应 TopK 或多模态能力各自达到批准的质量/成本门槛。
- embedding/model 升级可 shadow、灰度、切换、回滚和对账，旧代清理有保护。
- 生产技术文档、部署手册、权限说明和专项总进度已更新。

## 7. 完成记录

| 字段 | 内容 |
|---|---|
| 状态 | `TODO` |
| 负责人 | 待定 |
| 开始日期 |  |
| 完成日期 |  |
| 验证命令/演练 |  |
| 验证结果 |  |
| 覆盖率 |  |
| 交付物 | 租户/ACL/RLS、placement、配额、HA/DR、高级检索、模型升级工具与文档 |
| 回滚或降级验证 |  |
| 遗留风险 |  |
