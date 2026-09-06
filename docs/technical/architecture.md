# 系统架构

本文描述 MathStudyPlatform 当前有效的技术架构。未完成工作见 [项目待办](../TODO.md)，历史时间点资料见 [归档索引](../archive/README.md)。资源中心完整目标见 [PostgreSQL + Qdrant 双数据库方案](resource-center-qdrant-architecture.md)。资源中心已连接文档异步入库与 P3 检索、引用和 Session：API 登记任务，独立 worker 解析并建立当前索引，查询使用管理员 query embedding、Qdrant、FTS/RRF、可选重排和两次 PostgreSQL 鉴权。质量、容量及 M3 验收结论以 [阶段验收口径](../plans/resource-center-qdrant/TEST-ACCEPTANCE-2026-09-06.md) 和对应阶段报告为准，测试环境结果不等于生产规模承诺。

## 系统边界

```text
Browser
  |
  v
React + Vite/Nginx
  |
  v
Go net/http API
  |-- PostgreSQL + pgvector
  |-- Redis
  |-- Optional Qdrant vector index (resource profile)
  |-- Local/Qiniu/S3 storage
  |-- OpenAI-compatible providers through Eino
  `-- Xidian IDS account verification

Independent vector-worker
  |-- PostgreSQL jobs, leases, outbox and manifests
  |-- Private Local/Qiniu/S3 document storage
  |-- PDF/DOCX/TXT/MD parser and deterministic chunks
  `-- Administrator embedding provider -> Qdrant generations
```

Go API 是唯一默认后端。旧 Python FastAPI、LangGraph、LiteLLM、SymPy 和 OCR 工作流不属于当前运行链路。

## 技术栈

| 层级 | 主要技术 |
|------|----------|
| 前端 | React 19、TypeScript 5.9、Vite 7、React Router、Redux Toolkit、Tailwind CSS |
| 交互与展示 | Framer Motion、KaTeX、ECharts、AntV G6、React Hook Form、Zod |
| 后端 | Go 1.25、`net/http`、pgx、go-redis |
| AI/Agent | CloudWeGo Eino、OpenAI-compatible ChatModel、持久化 provider/model/Agent 配置 |
| 数据 | PostgreSQL 18、pgvector、Redis 7；可选 Qdrant `v1.14.1`（resource profile） |
| 交付 | Docker、Docker Compose、Nginx、Prometheus text exposition |

具体版本以 [backend/go.mod](../../backend/go.mod) 和 [frontend/package.json](../../frontend/package.json) 为准。

## 前端分层

```text
frontend/src/
├── app/          # Provider、路由和应用装配
├── pages/        # 学生、教师、管理员、公共页面
├── modules/      # 业务模块及其组件、Hooks、Service、状态和类型
├── components/   # 通用 UI、布局、图表和聊天组件
├── store/        # Redux Toolkit 根 Store
├── libs/         # HTTP、SSE、数学渲染、验证和导出
├── hooks/        # 跨模块复用逻辑
└── types/        # 公共 API 与模型类型
```

页面保持为组合层，业务逻辑进入模块 Hook 或 Service。模块外部通过 `index.ts` 公共接口访问，避免深层路径耦合。

## Go 后端分层

```text
backend/
├── cmd/api/                    # API 入口和依赖装配
├── cmd/migrate/                # 数据库迁移入口
├── cmd/vector-worker/          # 独立文档入库、对账和保留期清理入口
├── internal/application/       # 用例编排和事务边界
├── internal/adapter/http/      # REST/SSE handler、鉴权和错误映射
├── internal/adapter/postgres/  # pgx Repository 和读模型
├── internal/adapter/qdrant/    # Qdrant REST adapter（唯一 provider 边界）
├── internal/adapter/documentparse/ # PDF/DOCX/TXT/MD 有界解析
├── internal/adapter/llm/       # Eino Agent 适配
├── internal/adapter/storage/   # 本地、七牛和 S3 存储
├── internal/integration/       # 西电账户验证等外部集成
├── internal/platform/          # 配置、HTTP 公共能力、缓存、指标和安全基础设施
└── migrations/                 # Go forward migrations
```

依赖方向以应用层接口为中心：HTTP 适配器负责协议转换，PostgreSQL、Redis、存储、Qdrant、LLM 和外部服务通过适配器接入，应用服务负责业务规则与事务编排。Qdrant client import 只允许出现在 `internal/adapter/qdrant` 和 `cmd` 装配边界。资源 `SearchService` 依赖 `SearchRepository`、可选 `SearchCandidateRetriever`/`SearchReranker` 与观测接口；`VectorRetriever` 只依赖 application vector port、query embedder 和 manifest resolver。API 在 `QDRANT_ENABLED` 时装配向量适配器，并把同一个 `SearchService` 作为 Session 的窄 `KnowledgeRetriever`，Session/Eino/HTTP 均不导入 Qdrant client。

`IngestionService` 在当前私有存储快照写文件前先登记 staging；资源、不可变文档版本、任务和 outbox 在 PostgreSQL 事务内登记。`IngestionWorker` 依赖对象读取、解析、分块、embedding 和 vector port，通过 `owner + attempt + lease` 围栏提交状态；外部解析和模型调用不占用数据库事务。每代索引使用独立 collection，写入后逐点验证 payload/hash 并核对数量，满足整代发布屏障后才在 PostgreSQL 原子切换当前代。切换前仍读取旧代，旧代向量保留 7 天；下线或删除先撤销 PostgreSQL 可见性，再异步清理各代向量。对账以实时 manifest 为准，修复缺失/错配并清理多余向量。

详细终态任务及资源入库 outbox 保留 30 天，最后一次任务结果保存在文档紧凑快照中，清理不丢失教师状态、失败重试路由和代发布屏障。超过 24 小时且未被文档、版本或资产引用的 staging 才可领取清理，使用独立删除租约与当前私有命名空间检查；已登记文档的原文件仍按业务保留，不因下线或向量退役而自动删除。该流程不递归扫描或删除存储目录。

| 层 | 负责 | 不负责 |
|----|------|--------|
| `cmd` | 进程入口、依赖装配、生命周期 | 业务规则 |
| `platform` | 配置、日志、HTTP 基础设施、缓存、指标和安全公共能力 | 具体领域用例 |
| `application` | 用例、权限、事务和领域流程编排 | SQL、HTTP DTO 和 provider 细节 |
| `adapter/http` | 路由、请求解析、响应与错误映射 | 复杂业务判断 |
| `adapter/postgres` | SQL、Repository、事务实现和读模型 | HTTP 协议语义 |
| `adapter/redis` | 缓存、限流、租约和可恢复的短期状态 | 唯一业务事实 |
| `adapter/llm`、`adapter/storage` | AI 与对象存储实现 | 向应用层泄露供应商协议 |
| `integration` | 微信、西电等第三方系统边界 | 绕过应用层直接修改业务数据 |

测试源码不属于永久目录结构。每次变更在生产代码完成后创建临时单元、集成或契约测试，验证并记录结果后删除；测试运行器配置可保留供后续重复使用。

## 核心领域

| 领域 | 主要职责 |
|------|----------|
| Auth/Admin | 登录、JWT/Cookie 兼容、用户、密码重置和平台设置 |
| Session/Exercise | 学习会话、题目生成、判题、诊断、错题和 DKT 更新 |
| Progress/Portrait | 掌握度、学习路径、统计、知识图谱和学生画像 |
| Classroom/Teacher | 班级、成员、题库、教学资源和教师分析 |
| Communication/Forum | 私信、班级通知、答疑线程、全站论坛和消息中心未读摘要 |
| Daily Question | 上海自然日固定题、班级统一/个性化分配、教师计划与公众号提醒 |
| Resource/Upload | 资源元数据、收藏、上传、对象存储和管理员运行时配置 |
| AI Config | provider、model、凭据和 Agent 运行配置 |
| Xidian/Security | 西电账户绑定、安全日志、告警、健康检查和指标 |

## API 与兼容契约

- 业务 API 默认使用 `/api/v1`，健康检查和指标使用明确的独立入口。
- `POST /resources/ingestions` 只对当前有效教师/管理员开放，接收一份 PDF/DOCX/TXT/MD、标题、章节、主题和客户端 UUID，成功返回 `202` 与可轮询状态。同一所有者和 UUID 的相同载荷幂等，载荷变化返回 `409`；模型、租户、知识库归属由服务器决定。列表/详情仅返回所有者文档和固定错误码，重试、下线、删除使用独立状态接口。原始文件内容与 URL 不进入状态响应，公开引用继续走 chunk 的当前授权读取。
- `POST /resources/search` 使用当前认证用户与服务端默认租户，调用方不能指定模型、向量、用户或租户。PostgreSQL 先得到最多 1000 个粗授权资源；超过时向量降级，不截断为完整结果。FTS 与向量并行召回后 RRF 融合，先授权再向可选重排模型提供正文；重排与邻接扩展后再次授权，以当前账户、ACL、发布/删除、版本、generation 和 manifest 加载最终正文。deny 优先于 owner/allow。结果含 citation、模式、降级原因与独立邻接块；无索引为空结果，退役模型不影响有效文本 FTS。
- `GET /resources/citations/{chunk_id}` 必须同时绑定知识库、版本与 generation，再次执行当前 PostgreSQL 授权；引用失效或撤权统一返回 `404 CITATION_UNAVAILABLE`，响应禁止缓存，旧引用不构成访问凭证。前端通过该接口展示页面与章节定位，不绕过检查跳往旧资源详情或原文件 URL。
- Session 首次、续聊、流式及历史重开统一传递 `knowledge` 模式、降级信息和实际送入 prompt 的引用。知识正文作为独立的不可信资料消息，固定 Tutor 规则禁止执行资料内指令。16 KiB 动态输入预算共同约束当前问题、模式、附件占用、历史与完整知识序列化；知识最多 8 KiB且按整块保留，固定系统规则另占模型输入容量。数据库只保存引用元数据；带旧知识引用的助手回复不再次进入模型历史，以免撤权后重放资料。
- Auth/Admin、Session/Exercise、Progress/Portrait、Classroom/Teacher、Resource/Upload、AI Config 和 Xidian/Security 的路由由对应 HTTP adapter 承接；实际路由注册是接口清单的代码事实来源。
- 全站论坛使用 `/api/v1/forum`，由独立 application service、HTTP adapter 和 PostgreSQL repository 承接。学生、教师和管理员可以读写论坛；教师只能精选自己当前所教学生发布的帖子，管理员不能设置或取消精选，但可以将违规帖子设为不可见，举报审核只允许管理员操作。不可见使用现有 `hidden` 状态保留帖子、回复和审计数据，普通用户列表不展示；管理员帖子列表默认使用“全部帖子”，并提供“可见帖子”和各状态筛选，其中“全部帖子”包含 `open`、`resolved`、`hidden` 和旧 `deleted` 状态，且管理员可读取复核内容所引用的受保护附件。管理员可调用 `POST /posts/{id}/restore` 恢复帖子，`hidden` 状态会恢复可见，已公开帖子按幂等成功处理，旧 `deleted` 状态不可恢复；`accepted_reply_id` 为空时恢复为 `open`、非空时恢复为 `resolved`。恢复只重新开放帖子及其保留回复，不重新打开已处理举报、不撤销通知已读状态，也不恢复隐藏时清除的精选状态。管理员另可调用 `DELETE /posts/{id}/permanent` 直接永久删除任意状态帖子，不要求先设为不可见，数据库事务会清理多态举报并依赖外键级联清理回复、点赞、收藏和论坛通知。帖子附件 URL 记录在 JSON 中，永久删除数据库记录不会自动删除对象存储文件，文件回收需由独立存储清理流程负责。查询层结合 `featured_by`、发帖学生当前 `class_enrollments` 和查看者身份计算有效精选：仅发帖学生当前班级成员及该班教师看到精选标记，其他用户和管理员按普通帖子查看；所有列表排序都在服务端分页前先按有效精选置顶，精选操作不改写全站通用的帖子更新时间。发布帖子时板块和类型可省略，服务端将其归入稳定默认板块 `learning-methods`（学习方法）并使用 `discussion` 类型；历史帖子仍保留原有板块和类型。学生和教师从消息中心普通进入论坛时先停留在帖子选择界面，只有携带论坛帖子 ID 的互动通知深链会直接打开详情；回复、点赞、`@` 提及、最佳答案和精选通知统一合并进消息中心摘要，打开对应帖子后按通知所有权标记已读，点赞撤销、最佳答案变更或精选撤销/替换时同步撤回已经失效的互动通知。帖子与回复的并发写入统一按帖子、回复顺序加行锁，举报目标校验和举报写入位于同一事务，避免不可见后继续产生互动或待审核记录。管理后台提供举报状态筛选、目标内容查看、举报处理、不可见、恢复和永久删除入口。
- 前端依赖的 JSON 字段名保持稳定；错误响应保留稳定的 `code`、`message` 和 HTTP 状态码。HTTP 与 SSE 的失败统一在前端协议边界归一化为 `AppError`，携带错误类别、业务码、状态码、用户可读消息、来源、是否可重试以及可选的请求编号和等待时间；HTTP 响应从 `X-Request-ID` 和 `Retry-After` 提取关联信息，SSE 建连失败与流内 `error` 事件也进入相同模型。页面使用统一反馈组件按网络、超时、认证、权限、校验、冲突、限流、服务不可用等语义展示消息和恢复操作，并在存在请求编号时显示该编号供排障；作为请求控制信号的取消统一归类为 `cancelled` 并保持静默。
- HTTP 客户端只对 `GET`、`HEAD` 和 `OPTIONS` 的 `429` 自动重试，优先遵循服务端 `Retry-After`，缺失时使用有界指数退避；`POST`、`PUT`、`PATCH` 和 `DELETE` 不自动重放，避免重复写入，页面只能通过显式操作重新发起可重试请求，冲突类错误则引导刷新数据。OpenAI Responses、练习生成、答案 OCR、每日一题生成和上传的固定窗口限流在 `429` 响应中返回 `Retry-After`；无法可靠给出恢复时间的日额度或并发限制不虚构该响应头。该错误反馈收敛只改变协议适配和前端展示，不涉及数据库结构或数据迁移。
- 教师仪表盘只展示能够从现有学习事实计算的指标。为保持 JSON 字段名兼容，当前数据模型无法真实支持的 `avg_completion_rate` 和 `pending_grading` 保留为可空字段并返回 `null`；前端不得将其渲染或导出为伪造的 `0`，范围完成率统一读取教师分析接口。
- JWT、Cookie、一次性刷新令牌、数据库账户状态、当前角色和 `auth_version` 共同构成认证边界。所有受保护 API 都在请求时核对当前账户；密码或账户状态变化会递增版本，使此前签发的 access/refresh token 立即失效。
- 本地上传访问入口保留 `/uploads/`，只接受当前有效 access token（Bearer 或仅作用于该路径的 HttpOnly Cookie）。本地对象在上传后立即记录上传者，并默认拒绝无法归属的文件；历史引用不自动推断所有权。上传者、私信/答疑参与者、公告收件人、AI 会话所有者、可见论坛内容查看者和已发布资源查看者按对象关系放行。调整路径时必须同步修改前端、认证 Cookie、存储配置和部署代理，边缘代理不得绕过 Go API 直接暴露上传目录。
- 管理员 AI 风控列表和教师学生列表的 `page` 最大为 `100`、`page_size` 最大为 `100`，HTTP adapter 与 application service 都必须维持该边界，避免深 OFFSET 查询。
- 未知 `/api/v1/*` 路径返回 JSON `404 NOT_FOUND`，不会回落到其他运行时。
- 学生学习统计和画像共用 `application/learningrange` 的北京时间窗口；`content_attempts` 及画像时间字段按 UTC 无时区约定解释，查询边界先转换为 UTC，日/周聚合再转换为北京时间，周序列保留范围起点所在的不完整周并以实际起点展示。每日一题的业务日期和调度独立使用上海自然日，两类模块通过练习事实协作，不互相读取业务表。
- 错题本把 `content_attempts` 和 `diagnosis_reports` 作为不可变学习证据，把每个学生、每道题唯一的 `mistake_review_tasks` 作为可变复习计划。错题库同样按学生和题目聚合，只用最新一条未归档错误作答展示题面、诊断和重做入口，题卡主内容直接渲染冻结题干而不是题库分组标题，错误次数仍统计该题全部历史错误作答；筛选、排序、总数和分页都基于聚合后的题卡。首次错误在 1 天后到期；到期答对后依次进入 3 天、7 天验证，三次成功才标记掌握，任一新错误都会以最新可信题面重置计划。“待复习”只投影 `due_at <= now` 的到期任务，未到期任务则在其 `source_attempt_id` 对应的错题库题卡上提供“提前练习”，精确读取仍携带任务 ID 和 revision。计划时间只决定一次作答能否按正式复习推进阶段，不限制学生随时重做仍可提交且未归档的错题；提前重做或重做已掌握题时，答对不推进复习阶段，答错仍重置为 1 天计划。每日一题首次答错后的同 assignment 仅在任务尚未到期时标记为“即时订正”，可以低权重计为第一次成功；到期后统一按正式复习的完整权重和阶段规则处理。后续正式复习提交携带任务 ID、revision 和 submission ID。错题库列表接口额外投影关联任务的状态、到期时间、阶段、验证次数和最近结果，并支持 `due_status`、`stage`、`error_count_min` 与白名单排序；这些均由既有任务和作答事实实时计算，不新增数据库字段。前端三个视图通过单一按钮打开筛选与排序面板，并将筛选、排序和分页写入 URL；题卡保持原有简略摘要和复习操作，详情按钮按需读取既有错题详情接口，在弹窗中按题面、参考解析、学生作答、诊断、复习计划、历史记录的顺序展示，其中参考答案与解析默认隐藏并由题目后的按钮展开，标题、关闭入口以及题目标题/难度/错误类型元数据固定在详情滚动视口顶部，失效记录返回可重试错误。
- 远端共享迁移账本只到 version 9，错题本最终数据库契约统一由 `0010_mistake_review_tasks` 原子交付。曾执行未发布草稿 10 至 13 的本地数据库必须先停止写入、备份并核对最终结构，再做仅迁移元数据的收敛；禁止删除 version 10 后重放合并迁移。
- 复习任务冻结题面和标准答案，避免题库编辑、删除或每日题关闭改变后续验证内容。任务提交在学生级事务锁内再次校验 revision，响应随 submission ID 持久化以支持安全重试；同一 revision 的并发不同提交只有一个能更新状态。到期计划复习和不带错题上下文的普通练习以全权重 `1` 更新 DKT；提前复习、已掌握题重做、没有当前计划的历史错题重做及每日题即时订正以低权重 `0.35` 更新，降低重复熟悉题对掌握度的放大效应，其中即时订正仍可按业务规则推进一次复习阶段。权重固化在 `content_attempts.mastery_weight`，同时作用于当前增量和后续 DKT 序列中的历史交互。每次作答仍进入统一 attempt、诊断和 DKT 链路，并统一固化实际判题所用的完整题目快照；最近交互序列只读取该不可变快照，不再回退到可编辑的题库内容。题目、每日题和作答快照的知识点集合统一约束为非空 JSON 数组；历史 JSON `null` 或其他标量由前向迁移校准，仍为空的集合归入稳定的系统“未分类”节点。该节点首次 DKT 计算以 `0.5` 为基准，因此低权重提前练习仍会实际改变掌握度，不会再只显示固定的兜底值。错题读取在迁移未完成或外部数据漂移时仍按空数组降级，不再因数组展开返回 500。解析请求同时绑定提交后的任务 revision，任务变化时返回 stale。复习计划本身不直接修改掌握度。
- 普通班级题和 AI 题、普通历史错题重做、正式复习任务使用三个独立幂等命名空间，客户端为每次点击生成 UUID。普通作答以学生和 UUID 唯一绑定题目、答案载荷及完整响应；历史重做再绑定原错误 attempt；正式复习则以任务和 key 唯一绑定任务 revision、原错误 attempt、答案载荷及完整响应。相同请求重试返回首次结果，同 ID 改换题目、原记录、任务 revision 或答案会返回冲突，因此不会重复新增 attempt、诊断、复习状态或 DKT 交互。每日题继续使用 assignment 与 submission key 的既有幂等规则。HTTP 层在申请 AI 风控租约和消耗 OCR 限流额度前先只读查询响应快照，命中时直接回放；未命中才进入判题，`SubmitAnswer` 仍会在事务前和学生级事务内复查，以封闭并发窗口。正式复习解析还必须绑定本次提交返回的 attempt；任务进入下一到期阶段后，上一阶段 attempt 的解析权限立即失效，当前阶段先提交后才能读取答案。迁移前已有正式复习 key 因无法可靠还原原载荷而使用保留摘要，任何旧 key 重试都返回冲突。合并后的 `0010` 只在学生的全部历史交互都有冻结作答题面或每日题快照时执行 DKT 回放，绝不以当前题目解释旧作答；其余旧普通作答只使用迁移时可取得的最佳证据建立稳定快照，不声称恢复原版本，也不回算既有 DKT 和画像。
- 归档按聚合题卡执行，不会删除原始作答和诊断；当前题卡对应题目的全部未归档错误 attempt 标记与任务 `archived` 状态在同一事务内写入并递增 revision，使已打开的复习页立即失效且不会在归档后重新露出更早的相同题目。精确重做提交始终携带代表题卡的 attempt ID，并在相同学生级事务锁内复核所有者、题目、错误诊断和未归档状态；归档先提交时，旧页面稳定收到冲突且不会写入新 attempt。重复归档同一已归档 attempt 成功但不再改写时间、revision 或任务状态，非当前且尚未归档的旧 attempt 也不能归档当前题卡，因此旧请求不会退休后来由新错误重开的计划。新的错误作答会重新显示该题并清除任务归档状态，建立新的 1 天计划。归档任务不参与待复习或已掌握列表；复习任务通过内容外键跟随管理员硬删除的题目级联清理。
- 管理员删除知识节点时，仓储在同一事务内锁定节点并检查题目、每日题与复习快照、诊断、会话、DKT、画像、学习目标和学生画像 JSON 投影；任一引用存在都返回 `409 KNOWLEDGE_NODE_IN_USE`，不会静默改写历史题面或删除学习证据。解除或显式迁移全部业务引用后，才删除节点及相邻知识图谱关系；系统“未分类”节点始终禁止修改和删除。
- `/progress/portrait-insights` 提供确定性的画像洞察：卡片主值统计学生全部练习，匿名班级对比只使用相同时间范围内的教师课程题。练习量、时长和活跃天数以全班其他同学（含零记录）为分母，正确率与知识点掌握度只比较达到各自练习次数和置信度门槛的有效样本；接口返回比较口径但不返回同伴身份。
- 画像行动由学生通过 `/progress/portrait-actions/{concept_id}/start` 显式开始，并独立保存在 `student_portrait_actions`；开始时冻结对应知识点的 DKT 尝试次数，完成度取当前次数与基线的差值，包括每日一题按冻结题目快照产生的有效练习。进行中的行动优先于最新推荐返回，不会因知识点离开当前薄弱项而丢失；已完成行动再次成为薄弱项时可显式开始新一轮，未完成行动的重复开始保持幂等。练习提交和每日一题模块均无需读写画像状态，画像也不读取每日一题业务表。
- `/portrait` 继续负责可选的 AI 文字解读。生成输入把“范围内行为数据”和“当前累计 DKT 掌握状态”拆开，结构化画像与 AI 解读统一通过 `application/masteryprojection` 计算不回写数据库的当前遗忘投影；范围内最近知识点从 DKT 状态及其最后练习时间读取，不反查可变题目或每日一题业务表。报告保存 `portrait_range` 和 `portrait_snapshot_at`，并通过 `portrait_revision` 乐观并发控制防止生成/删除互相覆盖；切换范围后旧报告会标记为不匹配。结构化画像、班级比较和行动入口不依赖 AI 是否配置或报告是否已经生成，删除报告也不会删除学习记录。

## 关键技术决策

- HTTP 使用标准库 `net/http` ServeMux；数据访问使用 pgx，Redis 使用 go-redis。
- AI/Agent 通过 Eino 和 OpenAI-compatible provider 接入，运行配置持久化到数据库。Provider 的纯主机根地址会幂等补齐 `/v1`，带路径的地址视为完整 API base 并原样保留，以兼容 `/v1beta/openai` 等非 `/v1` 端点；Tutor 会话启用 Eino 上游流式输出，模型分片经 application callback 直接写入并刷新 SSE，流式请求使用 Chat Completions。非流式推理模型（含 provider 命名空间下的 `gpt-5*`、`o1*`、`o3*`、`o4*`）省略采样参数并优先使用 Responses，端点明确不支持时回退 Chat Completions。另提供 Bearer 鉴权的 `POST /v1/responses` OpenAI 兼容入口：请求 `model` 是数据库中的逻辑模型名，调度器复用 provider 优先级、权重、密钥轮换、供应商模型映射、超时和候选重试；渠道原生支持 Responses 时透传 JSON/SSE，否则通过独立兼容层转换为 Chat Completions，并在已向客户端交付首个流事件后禁止切换渠道。默认模型客户端共享进程级安全 Transport 和连接池，单请求仍保留独立总超时，单 provider 主机最多保留 20 条空闲连接。Top P 已从管理端和运行时停用；管理端保存的建议值为 Temperature `1.0`、Max Tokens `4096` 和最大重试 `3` 次，但 Agent 只在显式覆盖时启用采样或应用层重试，Responses 兼容入口则使用模型级重试基线。模型请求总超时默认 `1800` 秒；Agent 迭代上限独立于网络重试次数。
- 对象存储仅使用管理员保存的加密数据库配置；未配置时运行时保持停用，保存前完成真实写入探测，成功后通过原子运行时快照即时切换，进行中的请求继续使用原快照，读取不会跨后端回退。
- 数据库只追加经过评审的 Go forward migration，不自动执行 down migration。
- 每日一题按上海自然日持久化唯一学生任务。个性化模式在教师候选题、教师已发布题库、Solver 验证 AI 三层来源中依次选择，每层优先匹配目标知识点而不把不匹配题提前排除；可恢复的后台准备失败按持久化重试次数在当天重扫，统一题未布置时也会低成本重扫以接收教师当天补排期。历史已创建的失败任务按原班级归属原地恢复补做，补做不恢复连续天数。
- 教师策略和自动提醒开关在班级锁内按请求字段合并；班级统一题可在暂无排期时启用，生效日无题时学生端明确显示“老师未布置”，不改用题库或 AI 兜底。统一日程保存使用 `schedule_version` 乐观锁，题面在排期时冻结，教师后续编辑题库不会改变已排期或已发放题目。班级统计按入退班历史名册确定日期口径，迁移时仍在班成员可回填，迁移前已离班且从未产生 assignment 的成员无法从现有数据恢复。每日题提醒通过独立的、无正文公众号任务事件持久化，均不创建站内通知。自动提醒按班级和上海日期唯一且只恢复 `skipped/dead`；每次手动点击创建独立事件，可连续发送多轮。低库存事件使用发送时的上海自然日重新核对库存，并可恢复 `skipped/dead`；已发送事件继续保留“该一道题阈值已提醒”的事实，只有库存补充后再次降至一道才生成新提醒。
- Go API 是唯一对外业务 API；资源入库由独立 Go `vector-worker` 进程执行，不保留 Python 运行时兼容层。PDF 解析通过 Poppler `pdfinfo`/`pdftotext` 子进程，DOCX/TXT/MD 使用有界 Go 解析，不执行 Office 宏或文档指令。
- 教师学生详情使用 PostgreSQL 聚合读模型：一次 CTE 读取授权、画像、教师范围统计、班级排名和知识点派生输入，再用一次合并的有限活动查询和一次错题查询完成响应；该读路径不改变存储结构或 JSON 契约。安全日志保留由 API 生命周期内的有界后台 worker 执行，归档/删除批次使用 `FOR UPDATE SKIP LOCKED`，多实例可并行续作。管理员数据库备份导入通过流式 JSON 解码和临时 JSONL 分表暂存，总暂存量硬限制为 100 MB；全部校验通过后按外键顺序批量写入，临时文件始终清理。
- JWT 保持 HMAC 签名及稳定的 issuer/audience/type 契约；access 和 refresh token 都必须携带正数 `auth_version`，缺失版本的历史令牌拒绝使用。邮件发送使用受配置和安全边界约束的 SMTP adapter。

## AI 与降级边界

七类生成 Agent 为 `tutor`、`portrait`、`diagnostician`、`math_solver`、`question_parser`、`question_generator` 和 `ocr`，另有可选 `resource_reranker` 调用 `/rerank`。运行时读取管理员数据库配置；重排未启用正常跳过，超时或非法输出回退 RRF。query embedding 只使用管理员 active 不可变版本，Voyage 请求标记 `input_type=query`；模型契约与索引不符时保留 FTS 降级，不自动改模型。

关键契约：

- `POST /session/start-chat` 和 `POST /session/{session_id}/chat` 是端到端模型流：首次聊天可先发送 `session_info`，随后发送一次 `task_info`、多次 `message` chunk，并仅在回复保存完成后发送 `message` done。客户端把 `session_info` 或 `task_info` 视为服务端已经接收本轮请求；为兼容元信息事件丢失或顺序异常，首个有效 chunk 或 done 也可作为接收确认。确认后的用户消息不会因流异常被当成未发送，也不提供“重试上一轮”或自动重放，用户只能以当前可编辑草稿发起新一轮。
- 会话任务取消通过 Go API 进程内的活动任务注册表定位，并同时校验任务所有者；只有派生任务 context 的首个取消原因确为用户停止时，才使用停止终态，已先发生的客户端断连、超时或其他取消仍归类为异常中断。`POST /session/task/{task_id}/cancel` 会等待中断回复持久化和 AI 并发租约释放尝试结束；租约清理失败单独写入结构化日志，不改变已经停止且落库成功的业务终态。显式停止结果在进程内保留 5 分钟短期 tombstone，使并发或网络重试的取消请求得到相同成功或失败结果。活动表和 tombstone 都不写入 PostgreSQL 或 Redis，进程重启后不可恢复，且只能取消命中同一 API 进程的任务。当前部署边界因此是单 API 实例；多实例部署若要维持停止能力，必须另行实现粘性路由或共享取消协调，不能把进程内注册表视为跨实例任务系统。
- 用户显式停止时，服务端终止上游生成并把已生成部分连同精确尾注 `> 已停止生成` 保存为本轮助手消息；模型、网络、超时或流传输异常中断时，则保存已取得部分并使用精确尾注 `> 生成已中断`。两类终态都结束本轮，不回放或重试上一轮。首次聊天会在相同收尾路径原子保存助手消息并完成首轮请求标记；若进程中断遗留的 claim 已过期且仍只有欢迎语和首条用户消息，下一次普通聊天会先用确定性助手消息 ID 补写 `> 生成已中断` 并完成首轮，历史结构已经变化时则拒绝猜测性修复。
- 回复生成期间，文本输入保持可编辑并只代表下一轮草稿；发送、回车提交、附件增删、语音、模式切换及其他会改变当前请求的操作保持锁定，直到本轮进入 done、停止或中断终态。
- `POST /v1/responses` 保持 OpenAI Responses 的响应对象与具名 SSE 事件，不复用会话聊天的 `message` 事件。原生成功事件逐帧透传，失败/错误事件先脱敏；Chat fallback 以状态机生成 `response.created`、文本/拒绝/函数调用增量与 done、输出项 done 和 `response.completed|incomplete`，只以元数据一致的原生终态事件或 Chat `[DONE]` 判断完整结束。请求取消、写入失败、超限事件或无终态 EOF 都关闭上游响应体；SSE 禁止 Go gzip 和 Nginx 缓冲。入口对顶层参数使用显式兼容清单，只开放函数工具，并拒绝无法进入现有内容审核链的文件/音频输入和 provider 端 reusable prompt。学生请求复用 AI 内容审核、并发槽和日额度，只在非流式响应体或成功终态写入下游后写共享配额账本；账本故障记录运维错误但不改写已交付响应，非学生不计入学生额度。
- 图片作答从当前写入后端及仍已配置的 Local/Qiniu/S3 命名空间回读 PNG、JPEG 或 GIF，并在完整解码、OCR 置信度和数学判定均可靠后才开启事务；失败不产生 attempt、diagnosis、learning session、DKT 或 profile 更新。
- 判题结果只有 `correct`、`incorrect`、`indeterminate` 三态；本地确定性比较不能覆盖的代数、三角、极限、导数、积分、方程/解集、矩阵和证明题可交给 Eino Math Solver，服务不可用、超时、无效输出或低置信度统一返回带阶段、原因和重试语义的降级结果。
- 无缓存解析时，Math Solver 不接收标准答案并独立求解；候选最终答案以及推导步骤需经过单独的 `solution_verification` 调用，未验证步骤不会返回给前端。
- 自主出题模型不可用或结构化输出非法时返回 `503 AI_GENERATION_UNAVAILABLE`，不保存题目。
- 外部 provider、上传地址和西电账户验证地址经过出站地址校验，默认阻断本地和内网目标。

尚未完成的能力与验收项只在 [项目待办](../TODO.md) 中维护。

## 数据与迁移

PostgreSQL 是业务、版本和权限数据源，Redis 用于缓存和运行时辅助状态，Qdrant 仅保存可重建的向量和最小 payload。数据库结构由 `backend/migrations/` 中的 Go forward migration 管理；`0017` 追加资源中心契约，`0018` 追加管理员不可变模型配置，`0019` 增加 `pg_trgm`/检索索引与 nullable 会话引用元数据，`0020` 追加入库幂等、任务关联与对账游标，`0021` 追加终态保留快照与未引用上传 staging。后续从 `0022` 起追加。历史 Alembic 链和开发期增量链已退出当前工作区。迁移规则见 [Go 数据库迁移策略](../../backend/migrations/README.md)。
