# P5 性能与质量

> 状态：`TODO`
> 里程碑：M5 性能质量达标
> 前置依赖：[P4 生产就绪](04-production-readiness.md) `DONE`
> 后续阶段：[P6 多租户、高可用与智能增强](06-multitenancy-ha-and-intelligence.md)

## 1. 阶段目标

使用固定、代表性的语料和查询集优化索引、collection、批处理、缓存、切块、融合与重排参数，使性能、质量和成本达到 P0 批准的 SLO。没有目标规模实测证据时，不凭开发小数据集修改生产参数。

## 2. 工作清单

- [ ] **P5-01 代表性数据固化**：冻结文档类型、长度、语言、权限分布、更新率、查询分布和无答案样本，记录版本与 checksum。
- [ ] **P5-02 索引对比**：在目标 Qdrant 版本支持范围内比较 HNSW、scalar/product/binary quantization、on-disk vectors/payload 和 sparse index，记录 build time、memory、disk、Recall 和 latency。
- [ ] **P5-03 Collection/shard/replica 调优**：验证共享粒度、payload index、shard、replication factor、optimizer/segment 和 cache 对性能与隔离的影响。
- [ ] **P5-04 入库批处理调优**：优化解析并发、embedding batch、Qdrant upsert batch、wait/ordering、backpressure 和重建速率，限制对在线检索的影响。
- [ ] **P5-05 缓存与预加载**：评估 embedding/query、授权候选、资源元数据和邻接 chunk 缓存；缓存键必须包含权限与 generation 边界。
- [ ] **P5-06 切块实验**：比较 chunk 大小、重叠、标题/页码结构、父子关系对 Recall、引用和 token 成本的影响。
- [ ] **P5-07 召回融合调优**：调整 FTS/向量 TopK、RRF 常数、来源权重、阈值和去重，防止只优化单一离线指标。
- [ ] **P5-08 Rerank 调优**：比较模型、候选数、超时、质量增益和成本，定义何时关闭或跳过。
- [ ] **P5-09 质量回归门禁**：自动生成固定评测报告，覆盖 Recall@K、MRR/nDCG、引用正确率、无答案、权限和降级。
- [ ] **P5-10 负载与稳定性测试**：执行查询、混合读写、重建和 soak，记录 P50/P95/P99、QPS、错误、资源、backlog 和成本。
- [ ] **P5-11 参数决策与发布**：为每项参数记录证据、选择、适用容量、回滚阈值和线上观察窗口，更新配置与运行手册。

## 3. 实验纪律

1. 每次实验只改变一组相关变量，保留基线、样本版本、随机种子、硬件、依赖版本和配置。
2. 质量、性能、成本和运维复杂度共同评估，不能只追求单一最高 Recall。
3. 所有结果按权限过滤后的最终输出计算，不用未授权候选抬高离线指标。
4. 缓存测试必须覆盖租户/用户/ACL/generation 变化和失效，不能只测命中性能。
5. 索引参数调整通过新 generation 构建和门禁切换，不原地破坏当前可服务索引。
6. 小型开发数据只用于可执行性验证，不作为生产选型结论。

## 4. 最小结果表

| 类别 | 必须记录 |
|---|---|
| 数据 | 文档、版本、chunk、向量数量，长度/类型/权限分布 |
| 环境 | Qdrant/client/Go 版本、节点规格、shard、replication factor、网络和存储 |
| 索引 | 类型、HNSW/量化参数、payload index、build/optimizer 时间、内存、磁盘 |
| 查询 | 并发、QPS、P50/P95/P99、timeout、error、degrade |
| 质量 | Recall@K、MRR/nDCG、引用正确率、无答案和权限通过率 |
| 入库 | 文档/分钟、chunk/秒、embedding 成本、backlog age、失败率 |
| 成本 | embedding/rerank 调用、计算、内存、磁盘和网络成本 |

## 5. 验证计划

- 使用版本化评测数据和可重复命令生成结果；数据含敏感内容时只保存受控位置和匿名摘要。
- 临时 benchmark/测试源码在记录结果后删除，不进入提交。
- 执行在线检索与后台重建混合负载，验证对 API 和 worker 的相互影响。
- 在缓存启用/禁用、rerank 启用/禁用、Qdrant 降级三种模式下重复质量与性能检查，并覆盖 payload filter 选择性和 snapshot 恢复后的冷启动。
- 进行至少一次达到目标时长的 soak，检查内存、连接、segment、任务和错误累积。
- 参数发布使用 generation 蓝绿流程并验证回滚。

## 6. 阶段退出条件

- 固定数据与查询集下达到 D-004、D-005 的全部门槛。
- 选定索引、量化、collection、shard/replication、批处理和缓存参数均有可重复证据。
- 混合负载和 soak 无持续资源泄漏、backlog 失控或权限缓存串扰。
- 质量报告能阻止明显相关性、引用、无答案和权限回归。
- 每项优化都有适用范围、监控指标和回滚阈值。

## 7. 完成记录

| 字段 | 内容 |
|---|---|
| 状态 | `TODO` |
| 负责人 | 待定 |
| 开始日期 |  |
| 完成日期 |  |
| 验证命令 |  |
| 验证结果 |  |
| 覆盖率 |  |
| 交付物 | benchmark、评测报告、参数决策、配置、质量门禁、运行手册更新 |
| 回滚验证 |  |
| 遗留风险 |  |
