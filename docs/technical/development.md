# 开发指南

## 环境要求

- Go 1.25.13（`go.mod` 声明 `go 1.25.0` 和 `toolchain go1.25.13`）
- Node.js 20 和 npm
- PostgreSQL 18 + pgvector + pg_trgm
- Redis 7
- Qdrant `v1.14.1`（仅在资源中心 vector profile/live smoke 时需要）
- Poppler 的 `pdfinfo`、`pdftotext`（PDF 入库需要；DOCX/TXT/MD 不依赖外部解析器）

版本变化时以 [go.mod](../../backend/go.mod)、[package.json](../../frontend/package.json) 和 [docker-compose.yml](../../docker-compose.yml) 为准。

## 首次启动

在仓库根目录创建本地环境文件：

```powershell
Copy-Item .env.example .env
```

启动后端前先执行迁移：

```powershell
Set-Location backend
go mod download
go run ./cmd/migrate
go run ./cmd/api
```

另开终端启动前端：

```powershell
Set-Location frontend
npm install
npm run dev
```

根目录 `start.bat` 可在 Windows 上同时打开前后端进程，但不会替代首次数据库迁移。

## 常用验证命令

Go 后端：

```powershell
Set-Location backend
go vet ./...
go build ./...
gofmt -w <changed-go-files>
```

前端：

```powershell
Set-Location frontend
npm run lint
npm run build
```

提交前在仓库根目录运行：

```powershell
git diff --check
git status --short
```

## 临时测试规则

仓库不永久保留或提交测试用例源码。生产代码完成后，才按本次变更创建临时 `*_test.go`、`*.test.ts(x)` 或 `*.spec.ts(x)`；测试范围覆盖公共行为、边界输入、错误条件和外部依赖降级，修改共享契约时同时做 Go 与前端临时契约验证。

测试运行器配置和依赖可以保留。临时测试存在时按需运行：

```powershell
# Go：只运行受影响包，必要时再扩大范围
Set-Location backend
go test <affected-packages> -count=1

# 前端：传入本次创建的临时测试文件
Set-Location ../frontend
npm test -- <temporary-test-path>
npm run test:coverage -- <temporary-test-path>
```

测试通过后先记录命令、结果和必要覆盖率，再按明确路径删除本次临时测试及其专用 fixture/mock；禁止使用宽泛递归删除。提交前在仓库根目录确认以下命令没有输出：

```powershell
git ls-files "*_test.go" "*.test.ts" "*.test.tsx" "*.test.js" "*.test.jsx" "*.spec.ts" "*.spec.tsx" "*.spec.js" "*.spec.jsx" "test_*.py" "*_test.py"
git diff --cached --name-only --diff-filter=ACMR | Select-String -Pattern '(_test\.go|\.(test|spec)\.(ts|tsx|js|jsx)|(^|/)test_.*\.py|_test\.py)$'
```

## 代码组织

### 前端

- 页面只负责布局和业务模块组合。
- API 调用放在 `src/modules/*/services/`，交互状态和业务流程放在模块 Hook 或 Store。
- 模块通过 `index.ts` 暴露公共接口，外部代码避免深层导入。
- 通用 UI 放入 `src/components/`，与业务绑定的组件留在对应模块。

### 后端

- `application` 表达用例、事务和业务规则。
- `adapter/http` 负责请求解析、鉴权、响应和协议错误映射。
- `adapter/postgres` 负责 SQL、扫描和持久化语义。
- `platform` 只承载跨领域基础能力，不放业务规则。
- 新外部依赖通过接口和适配器接入，并在临时测试中替换为 fake 或 mock。

完整协作约束见 [AGENTS.md](../../AGENTS.md)。

## 数据库迁移

新增迁移文件使用 `NNNN_description.up.sql` 命名，并放在 `backend/migrations/`。当前只使用 forward migration：

```powershell
Set-Location backend
go run ./cmd/migrate
go run ./cmd/migrate  # 重复执行应无待应用版本
```

当前迁移链是 `0001` 至 `0021`。`0017` 建立资源中心版本/chunk、generation、job 和可靠 outbox 基础，`0018` 提供管理员 embedding 不可变配置，`0019` 增加 `pg_trgm`、检索索引与会话引用元数据，`0020` 补齐入库任务、幂等和对账游标，`0021` 补齐终态任务快照与上传 staging。空库首次记录 version 1 至 21，version 19 库顺序应用 20、21，version 20 库只新增 21，复跑无待应用版本。迁移账户须可在 `public` 安装 `pg_trgm` 并创建索引；先停止 API/worker 写入、迁移，再启动新进程。曾执行旧草稿 10 至 13 或旧错题草稿占用 version 11 的本地库，按 [迁移策略](../../backend/migrations/README.md) 校准，不能删除账本重放。runner 校验版本、名称和未知记录；后续从 `0022` 起追加。

## 环境配置

仓库根目录 `.env` 是本地和部署环境的统一文件名，`.env.example` 是唯一模板。至少应按环境修改：

- PostgreSQL、Redis 和连接池配置
- JWT、Fernet、管理员初始化凭据
- CORS 和管理端允许网段
- 安全日志归档、删除期限，以及自动清理的周期、超时和批次上限
- 管理端数据库备份导入使用流式 JSON 校验和临时分表暂存，暂存总量硬限制为 100 MB；导入完成后临时文件会自动清理，不需要手工回收
- Eino provider 的兼容配置
- 本地存储根目录 `UPLOADS_DIR`；对象存储后端和云存储凭据由管理员保存到数据库，不写入 `.env`
- 西电账户绑定端点和超时
- 微信公众号凭据、回调消息模式和外部请求超时
- Qdrant 仅在启用向量能力时配置：`QDRANT_ENABLED`、`QDRANT_URL`、`QDRANT_API_KEY`、`QDRANT_COLLECTION`、payload index 字段、请求/健康超时、批量大小和 wait-for-changes。开发默认关闭；非开发环境开启时必须提供 API key，日志和错误不得记录 key。

不要提交 `.env`、API key、密码或真实用户数据。

### 本地 Qdrant profile

核心 Compose 栈不会自动启动向量服务。仅运行宿主机 API/worker 时，在根目录启动 Qdrant：

```powershell
docker compose --profile vector up -d qdrant
docker compose --profile vector ps qdrant
```

将后端 `.env` 中的 `QDRANT_ENABLED` 设为 `true`，容器内访问地址使用 `http://qdrant:6333`（宿主机运行 Go API 时使用 `http://localhost:6333`）。本地 `QDRANT_API_KEY` 留空时，Compose 会在启动 Qdrant 前移除空的服务端 key 环境变量，避免 Qdrant 把“存在但为空”解释为已开启鉴权；设置非空 key 时，healthcheck 和 adapter 都使用该 key，不能把值写入日志或文档。healthcheck 使用镜像自带 Bash 的 `/dev/tcp`，不依赖镜像中不存在的 `curl`。collection 的维度、距离与 payload index 由 worker 按管理员 active 模型和 generation 显式创建/校验，适配器不会猜测模型参数。停止完整 profile 使用 `docker compose --profile vector stop vector-worker qdrant`，不要删除 PostgreSQL 或 Qdrant 数据卷。

### 文档入库与独立 worker

管理员先保存私有 Local/S3/七牛存储，并测试、激活向量模型。API 与 worker 必须使用同一数据库、`FERNET_SECRET_KEY` 和 `UPLOADS_DIR`；worker 每次读取/清理对象前刷新管理员存储配置，切换存储不自动搬迁历史文件。缺少 active 模型时上传返回 `503 EMBEDDING_UNAVAILABLE`，不开启随机模型回退。

本机从可信 Poppler 发行包安装 `pdfinfo` 和 `pdftotext`，不能只安装含 `pdfinfo`/`pdftoppm` 的裁剪包。两个程序可放在 PATH，或通过绝对路径传入；容器运行镜像已安装 `poppler-utils`。在 `backend/` 中运行：

```powershell
go run ./cmd/vector-worker run --concurrency=2
# Windows 自定义 Poppler 位置示例
go run ./cmd/vector-worker run --pdfinfo='C:/tools/poppler/Library/bin/pdfinfo.exe' --pdftotext='C:/tools/poppler/Library/bin/pdftotext.exe'
Invoke-RestMethod http://127.0.0.1:8091/health
Invoke-WebRequest http://127.0.0.1:8091/metrics
```

`run` 持续消费真实任务并执行周期维护，不接受 `--apply`/范围参数。默认并发 2（1-8）、轮询 1 秒、任务租约 60 秒并每 10 秒续租；CLI 的单任务预算为 10 分钟。失效 owner 不能提交发布，进程退出后由过期租约接管。瞬态失败按任务次数指数退避，默认最多 3 次自动领取，之后进入 `dead`；解析错误进入 `failed`，由教师按状态允许的“重试”重新提交。缺少 Poppler 只影响 PDF，不会把扫描件、加密或超页文档误报成功。

教师资源页的文件入口对 PDF/DOCX/TXT/MD 调用 `POST /api/v1/resources/ingestions`，multipart 仅包含 `file`、`title`、`chapter`、`topic`、`client_request_id`。每份文件最多 50 MiB，每批界面最多 10 份，解析最多 200 页/200 万字符。成功返回 `202` 后在“文档处理”页轮询，不代表已经可检索。GET 集合/详情、POST `/{resource_id}/retry`、POST `/{resource_id}/unpublish`、DELETE `/{resource_id}` 均限文档所有者；状态接口禁止缓存，操作按钮以 `can_retry/can_unpublish/can_delete` 为准。已入库正文不可通过旧资源编辑覆盖，标题/章节/主题等元数据仍可修改。

同一文件与元数据在网络失败后复用客户端 UUID，改变载荷需生成新 UUID。持久化 staging 必须先于文件写入；文件与登记身份使用同一个存储快照。服务端当前默认知识库统一归属，普通请求不接受模型或租户覆盖。分块为确定性 NFC 文本；页码未知时为 0，不伪造 PDF 页码，字符偏移按规范化文本的 Unicode 字符计数，token 数是保守 UTF-8 字节上界。文档 embedding 使用 active 不可变模型契约，Voyage 发送 `input_type=document`，每请求最多 32 段且总输入不超过 110000 UTF-8 字节。

维护命令默认 dry-run，先查看 JSON 报告中的差异、范围及 `complete`，再对明确范围应用：

```powershell
go run ./cmd/vector-worker reconcile --generation='<generation-uuid>'
go run ./cmd/vector-worker reconcile --generation='<generation-uuid>' --apply
go run ./cmd/vector-worker rebuild --knowledge-base='<knowledge-base-uuid>'
go run ./cmd/vector-worker rebuild --knowledge-base='<knowledge-base-uuid>' --apply
```

`reconcile --apply` 必须指定 canonical generation UUID；可额外用 `--knowledge-base` 收窄范围。`rebuild` 必须指定知识库且始终读取管理员当前 active 模型，创建独立新代任务后由 `run` worker 完成，旧代持续服务直至原子切换。`--max-pages` 默认 200、范围 1-10000；`--timeout` 默认 2 分钟、范围 1 秒至 10 分钟。`complete=false` 表示还有后续页，应用模式保存游标并在下一轮续作，不能把部分扫描当作零差异验收。

持续维护默认每 5 分钟执行（`--reconcile-interval`），覆盖缺失/错配修复、多余向量删除、7 天退役代向量保留和 30 天终态 job/outbox 回收；每轮最多清理各 1000 条历史记录，并保留文档最后任务快照。上传 staging 超过 24 小时且没有任何文档/版本/资产引用才领取，单轮最多 8 个、删除租约 15 分钟。已登记、下线或删除文档的原对象不属于未引用 staging，不能借此批量删除业务文件。回收失败记录固定指标/错误码，之后按租约重新接管，禁止把来源 URL、签名、正文或密钥写入日志。

后台 AI provider 的 `base_url` 可以填写纯主机根地址或完整 API base；纯主机地址会自动补 `/v1`，只要地址中已有路径就会原样使用，因此 `/v1`、`/proxy/v1`、`/v1beta/openai` 均不会被重复改写。非流式调用会自动兼容 Chat Completions 与 Responses，推理模型按大小写不敏感的 `gpt-5*`、`o1*`、`o3*`、`o4*` 前缀识别，也兼容 `provider/model` 命名空间，并优先尝试 Responses。连接测试对推理模型使用 `max_completion_tokens=32`，对旧式 Chat provider 保留 `max_tokens=32`。

管理端在渠道或智能体配置完整保存后显示成功反馈，智能体配置保存成功后自动收起当前配置框；保存失败时保留当前表单并显示服务端原因。渠道编辑只有在渠道信息和模型列表都更新成功后才算完成。“已保存”仅表示配置已持久化，“已配置”还表示智能体存在启用的候选渠道，但两者都不代表外部模型可调用；真实连通性仍以渠道的“测试连接”结果为准。

资源向量模型由管理员在“AI 模型设置 -> 向量模型”中选择已启用渠道模型；仅模型为必填项，测试自动识别实际维度，revision 由系统内部生成，高级参数收纳在可折叠区域。自动 revision 的指纹同时绑定渠道、API base、模型来源版本和完整向量/运行契约；显式 revision 若对应的来源或契约不同则返回冲突，已写入的不可变版本不会被覆盖。测试与激活接口使用受控 HTTPS 出站客户端，校验 `/v1/embeddings` 的响应顺序和实际维度；多 API Key 渠道必须逐个验证全部 Key，并要求返回维度一致，网络错误、HTTP 408/429 和 5xx 按“瞬时错误最大重试次数”有限退避，整次验证共享最多 20 次额外重试和一个总超时。测试与激活都会产生真实上游调用，管理端测试按钮提供费用提示。通用 OpenAI 兼容请求不发送可选 `encoding_format`，避免不兼容上游返回 HTTP 400。凭据继续只加密保存在渠道记录中，不复制到 embedding 版本或响应。激活会在事务中退役旧版本并保证 `resource_embedding` 最多一个 active；无 active、渠道/模型停用或探针后来源发生变化时，运行时失败关闭，必须由管理员重新测试并激活，不能由代码、环境变量或普通请求回退覆盖。

管理员于 P2-A 激活 `voyage-4-large` 系统版本 `auto-v2-e5ec9a9f2abaa010`（1024 维、Cosine、`send_dimensions=false`、32/30/3）；这只是当时的验证记录，运行时始终读取当前 active 配置。P3 查询会校验 active 契约与当前 generation 一致，Voyage 使用 `input_type=query`。重排通过管理端智能体 `resource_reranker` 配置，未启用时跳过，失败保留 RRF 顺序；只接受 `/rerank` 的索引/分数响应。常规临时测试 Mock 外部依赖；真实质量和容量评测仅使用隔离测试库、已审查原创语料和明确的模型调用预算，评测口径与证据见 [阶段验收口径](../plans/resource-center-qdrant/TEST-ACCEPTANCE-2026-09-06.md)。

P3 的 `POST /api/v1/resources/search` 最小 JSON 为 `{"query":"导数"}`；支持 `knowledge_base_id`、`top_k`（1-20，默认 5）、`timeout_ms`（100-10000，默认 3000）与 `filters.type/chapter/topic`。16 KiB 请求体拒绝未知字段和尾随 JSON，身份与 trace 来自中间件。响应含 `items`、可选 `adjacent`、`mode`、`degraded`、`degraded_reasons`、`trace_id`；每项带完整正文和 knowledge-base/resource/version/chunk/generation/page/section/title/hash 引用。FTS 使用加权 `simple` 词项与汉字查询的转义子串匹配；模型退役仍可检索有效已发布文本。向量关闭/失败时 FTS-only，重排失败时保留融合；没有当前索引或授权候选时为空，最终授权失败固定 503，参数错误 400，超时 504。

`GET /api/v1/resources/citations/{chunk_id}?knowledge_base_id=...&document_version_id=...&generation=...` 返回重新授权的 `SearchHit`；失效/撤权统一 404，禁止缓存。资源中心“知识搜索”和聊天引用均使用该入口。Session 请求检索最多 1500 ms，知识最多 8 KiB，并与问题、模式、附件和历史共享原有 16 KiB 动态输入预算；固定系统规则另占模型容量。整块无法容纳则跳过，引用只对应实际输入。首次、续聊、SSE done 与历史返回相同 `knowledge` 元数据；不会在会话表保存资料正文，也不会把旧知识助手回复再次注入历史。无知识或检索失败时继续普通聊天。

`/metrics` 提供 `msp_resource_search_requests_total`、阶段耗时直方图、候选/过滤/引用/空结果计数及降级原因计数。标签为固定 mode/outcome/stage/source/reason，不含查询、用户、资源、trace 或 provider 原始错误。结构化检索日志只记录状态、时长和数量。完整验证证据和边界见 [P3 计划](../plans/resource-center-qdrant/03-retrieval-and-rag-integration.md)。

管理端的智能体参数覆盖不再提供或发送 Top P。新发现模型保存 Temperature `1.0`、Max Tokens `4096`、超时 `1800` 秒和最大重试 `3` 次作为配置基线；其中 Temperature、Max Tokens 和最大重试默认不启用，输入留空时前两项不写入 provider 请求且应用层不重试，只有显式覆盖才生效。超时留空时使用模型的 `1800` 秒总请求时限；这与 Cherry Studio 流式请求收到数据后重新计时的 idle timeout 并不完全等价。Agent 的 `MaxIterations` 固定使用独立默认值 `8`，不得再从重试次数推导。数值和开关语义参考 Cherry Studio 当前的 [Assistant 默认设置](https://github.com/CherryHQ/cherry-studio/blob/12498d68ecb4fb261670843ca7a8e4e64a37526a/src/shared/data/types/assistant.ts)、[请求超时](https://github.com/CherryHQ/cherry-studio/blob/12498d68ecb4fb261670843ca7a8e4e64a37526a/src/main/ai/constants.ts) 和 [模型重试策略](https://github.com/CherryHQ/cherry-studio/blob/12498d68ecb4fb261670843ca7a8e4e64a37526a/docs/references/ai/model-retry.md)。`0014_ai_generation_defaults` 会清空历史 Top P，并只校准仍使用旧默认值的模型；显式自定义的其他数值不变。数据库中的旧 Top P 列和后端兼容 JSON 字段暂时保留，但运行时一律忽略。

学习会话的 `POST /session/start-chat` 和 `POST /session/{session_id}/chat` 使用真正的模型分片流，而不是等待完整回复后再包装为 SSE。事件顺序为可选的 `session_info`、一次 `task_info`、多次 `message` chunk、一次 `message` done；每个事件写入后立即 flush。前端以 `session_info` 或 `task_info` 确认服务端接收，缺失这两类元信息时以首个 chunk 或 done 兜底确认；确认后的轮次不会因断流恢复成待发送内容，也不自动重放。Tutor 流式请求直接使用 provider 的 Chat Completions 流，Responses 自动转换继续只用于非流式 Agent。默认模型请求共享进程级 HTTP Transport 和连接池，但通过客户端浅拷贝保留每次运行配置的独立总超时；显式注入的测试客户端仍按请求单独包装。

生成期间输入框仍可编辑下一轮文字草稿，但发送、回车提交、附件增删、语音、模式切换和当前轮次相关操作保持锁定。停止按钮使用 `task_info` 中的任务 ID 调用取消接口；活动任务只登记在处理该流的 Go API 进程内，因此本地和当前生产拓扑按单 API 实例验收，多实例环境不能假设任意实例都能取消任务。取消只在用户停止成为任务 context 的首个原因时生效，并等待中断回复持久化及 AI 并发租约释放尝试完成；停止终态在进程内保留 5 分钟，使重复取消短期幂等，进程重启后不保留。前端在取消接口成功后优先等待原聊天流的 `cancelled` 事件消费完末尾分片，事件丢失时才超时兜底，取消接口失败则保持原流并明确提示。

显式停止会保存已生成部分并追加独立 Markdown 引用行 `> 已停止生成`；异常中断保存已生成部分并追加 `> 生成已中断`。已确认接收的停止或中断直接保留本地已收分片并刷新会话列表，不用即时历史读取覆盖界面；只有请求已经发出但尚未收到接收事件时，才按稳定会话 ID 做只读历史探测。不显示或执行“重试上一轮”，用户在生成期间输入的下一轮草稿保持不变。若 done 与首个接收确认位于同一批 SSE 数据中，输入 ref 会在结算前同步区分已发送内容和真正的下一轮草稿，避免错误停留在 `/session/new`。

首次聊天仍以客户端生成的会话 UUID 作为幂等身份。`session_info` 表示欢迎消息和首条用户消息已经原子落库，停止、中断和正常 done 都必须在同一完成事务中保存首轮助手消息及完成标记。若客户端在任何接收事件前断开，会先按该 UUID 探测历史：确认不存在才丢弃身份，已经物化则转为现有会话，输入和附件仍由用户当前草稿持有。若进程中断遗留的 claim 已过期且仍未产生助手消息，下一次普通聊天会先以确定性助手消息 ID 补写 `> 生成已中断` 并完成首轮；历史结构不再是欢迎语加首条用户消息时返回冲突，不猜测或重复生成。

## OpenAI Responses 兼容接口

`POST /v1/responses` 接受平台 access token 作为 Bearer token；`model` 填管理端模型的逻辑名称而不是供应商 `model_id`。入口支持非流式 JSON 和 `stream=true` 的具名 SSE，常用字段 `input`、`instructions`、函数 `tools`、`tool_choice`、`temperature`、`top_p`、`max_output_tokens`、`parallel_tool_calls`、`reasoning.effort` 和 `text.format` 会原生透传，或在 Chat fallback 中转换为对应字段。入口只接受兼容层列出的 OpenAI Responses 顶层字段，未知供应商扩展返回 `unsupported_parameter`；只开放函数工具，文件、音频输入和 provider 端 reusable prompt 因无法进入现有内容审核链而统一拒绝。模型 `capabilities` 可用布尔键 `responses`、`chat_completions`、`tools`、`temperature` 明确关闭不支持的能力；未声明时按 OpenAI-compatible 能力尝试。

本服务不实现 Response 查询、取消或后台任务端点，因此强制原生请求默认 `store=false`，并对 `store=true`、`background=true`、`previous_response_id`、`conversation` 和 reusable `prompt` 返回明确的 `unsupported_parameter`。Chat fallback 支持文本、用户图片和 JSON Schema 格式；其他供应商专有状态字段会返回明确错误，而不会静默丢弃。上游失败只会在首个流事件交付前按模型级 `default_max_retries` 切换候选；兼容供应商没有统一幂等协议，重试可能产生额外 token 成本，所以对有外部副作用的函数工具应由调用方或工具执行层使用业务幂等键。学生额度只在非流式响应体或 `response.completed|incomplete` 终态成功写入下游后计入；配额账本短暂写入失败会记录脱敏运维错误，不会把已经交付的模型结果改写成可重试的 5xx 或尾随 `error` 事件。

本地联调可直接请求 Go API；通过 Vite 或生产前端容器时 `/v1/` 也会代理到后端：

```powershell
$headers = @{ Authorization = 'Bearer <platform-access-token>'; 'Content-Type' = 'application/json' }
$body = '{"model":"<logical-model>","input":"计算 2+2","instructions":"简洁回答","max_output_tokens":128}'
Invoke-RestMethod -Method Post -Uri http://localhost:8000/v1/responses -Headers $headers -Body $body

$streamBody = '{"model":"<logical-model>","input":"计算 2+2","stream":true}'
curl.exe -N http://localhost:8000/v1/responses -H "Authorization: Bearer <platform-access-token>" -H "Content-Type: application/json" -d $streamBody
```

## 微信公众号测试号联调

公众号回调由微信服务器从公网发起，不能直接填写 `localhost`。本地联调需要同时运行 PostgreSQL、Redis、Go API 和临时 HTTPS 隧道；前端在测试学生或教师绑定时需要运行。建议先使用微信公众平台接口测试号，正式公众号仍需单独验收主体权限。下文以默认 `API_V1_PREFIX=/api/v1` 为例；若修改了该配置，所有回调和调试 API 路径都要同步替换前缀。

先将测试号凭据写入本机 `.env`：

```dotenv
WECHAT_OFFICIAL_ACCOUNT_ENABLED=true
WECHAT_OFFICIAL_ACCOUNT_APP_ID=<test-app-id>
WECHAT_OFFICIAL_ACCOUNT_APP_SECRET=<test-app-secret>
WECHAT_OFFICIAL_ACCOUNT_TOKEN=<3-to-32-ascii-letters-or-digits>
WECHAT_OFFICIAL_ACCOUNT_AES_KEY=
WECHAT_OFFICIAL_ACCOUNT_MESSAGE_MODE=plain
WECHAT_OFFICIAL_ACCOUNT_NAME=微信接口测试号
WECHAT_OFFICIAL_ACCOUNT_HTTP_TIMEOUT_SECONDS=10
WECHAT_MESSAGE_REMINDERS_ENABLED=false
WECHAT_PRIVATE_MESSAGE_TEMPLATE_ID=
WECHAT_NOTICE_TEMPLATE_ID=
WECHAT_QA_MESSAGE_TEMPLATE_ID=
```

`APP_SECRET`、`TOKEN` 和 `AES_KEY` 都是密钥，不得提交、粘贴到 issue 或出现在共享截图中。截图、日志或聊天记录一旦暴露密钥，应先在微信后台重置对应值，再继续联调。

回调模式必须与微信后台的消息加解密设置一致：

| `WECHAT_OFFICIAL_ACCOUNT_MESSAGE_MODE` | 微信后台模式 | 后端接受的回调 | `AES_KEY` |
| --- | --- | --- | --- |
| `plain` | 明文模式 | 仅明文签名和明文 XML | 可留空 |
| `compatible` | 兼容模式 | 明文或 AES 加密 XML | 必填，微信后台生成的 43 字符值 |
| `safe` | 安全模式 | 仅 AES 加密 XML | 必填，微信后台生成的 43 字符值 |

若测试号页面没有消息加解密模式选项，使用 `plain`。不要自行编造 `AES_KEY`，兼容模式和安全模式必须使用微信后台对应的 `EncodingAESKey`。

消息中心与微信公众号基础分别由 `0003`、`0004` 交付；运行前应用当前全部 `0001` 至 `0021` 迁移。空库首次记录 21 个版本，已有库只新增尚未应用的版本，第二次运行无待应用版本。

```powershell
Set-Location backend
go run ./cmd/migrate
go run ./cmd/migrate  # 应返回 applied_count=0
go run ./cmd/api
```

另开终端建立临时 HTTPS 隧道，例如已安装 `cloudflared` 时：

```powershell
cloudflared tunnel --url http://localhost:8000
```

将命令输出的 HTTPS 根地址拼接固定回调路径，并填入测试号“接口配置信息”的 URL：

```text
https://<temporary-host>/api/v1/integrations/wechat/official-account/callback
```

测试号页面的 Token 必须与 `.env` 中 `WECHAT_OFFICIAL_ACCOUNT_TOKEN` 完全一致。临时隧道停止后不可访问，重新启动通常会生成新域名；URL 每次变化都必须回到测试号后台重新填写并提交验证。JS 接口安全域名不参与服务器回调，基础联调可以留空。

按以下顺序验收，不应把代码构建通过写成真实微信验收通过：

1. 在测试号后台提交 URL 和 Token，确认 `GET` 回调验证成功。
2. 用个人微信扫描测试号二维码关注，确认 `subscribe` 回调到达且收到被动回复。
3. 使用学生或教师账号登录前端，在个人中心生成一次性绑定口令，并向测试号发送完整的“绑定 XXXX-XXXX”命令；两种角色应分别验收一次。
4. 确认微信收到“绑定成功”被动回复，刷新个人中心后显示已绑定和已关注。
5. 使用管理员访问令牌调用 `POST /api/v1/admin/wechat/test-message`，JSON 只传 `{"user_id":"<student-or-teacher-id>"}`；该接口发送服务端固定内容，不接受管理员自定义消息。
6. 取消关注后确认绑定记录仍保留但订阅状态变为未关注；重新关注后状态恢复，必要时再验证解绑和换绑冲突。

同一平台账号每 10 分钟最多生成 3 个绑定口令，超限返回 `429 WECHAT_BINDING_RATE_LIMITED`。口令首次由某条微信消息使用时会按该消息事件 ID 原子预留，不会在数据库绑定前直接删除；进程中断后，同一条微信重试仍可完成绑定，其他消息不能复用该口令。回调处理中使用 6 秒短租约，完成后保存 24 小时去重结果和被动回复；并发重试在处理中返回 503，完成后的重试会重放同一回复。POST 回调整体响应有 4.5 秒硬上限，避免超过微信 5 秒窗口。

基础绑定验收通过后，在测试号后台分别新增三份模板。模板标题可写“新私信提醒”“班级通知提醒”“答疑消息提醒”；每日一题学生提醒和统一题低库存提醒复用“班级通知提醒”模板。模板正文必须使用以下字段名；文字标签可以调整，但不能把 `keyword1`、`keyword2`、`keyword3` 改成其他名称：

```text
发送人：{{keyword1.DATA}}
主要内容：{{keyword2.DATA}}
发送时间：{{keyword3.DATA}}
```

```text
发布人：{{keyword1.DATA}}
通知主题：{{keyword2.DATA}}
发布时间：{{keyword3.DATA}}
```

```text
发送人：{{keyword1.DATA}}
主要内容：{{keyword2.DATA}}
发送时间：{{keyword3.DATA}}
```

把三份模板各自生成的模板 ID 写入 `WECHAT_PRIVATE_MESSAGE_TEMPLATE_ID`、`WECHAT_NOTICE_TEMPLATE_ID` 和 `WECHAT_QA_MESSAGE_TEMPLATE_ID`，再将 `WECHAT_MESSAGE_REMINDERS_ENABLED` 改为 `true` 并重启 Go API。总开关开启时三个模板 ID 都必须配置；技术上允许多个配置使用同一个模板 ID，但正式验收建议保持三份独立模板。随后按时间顺序验证：

1. 学生向教师发送私信，教师收到私信模板卡片；教师向学生回复时学生收到同类卡片。`keyword1` 是发送人，`keyword2` 是主要内容，`keyword3` 是北京时间。
2. 教师发布班级通知，每个发布时成员快照中的学生各产生一个任务；`keyword1` 是发布人，`keyword2` 只展示通知主题，`keyword3` 是发布时间。
3. 学生发起答疑时教师收到答疑模板卡片；教师回复和学生追问均提醒另一方。字段含义与私信相同。
4. 教师开启每日一题自动提醒后，确认当天有 `ready` 且未完成题目的学生只收到公众号模板消息，平台内不生成通知；无题时不创建任务。手动与自动提醒使用固定且相互独立的来源；关闭再开启或定时对账时，自动来源的 `skipped/dead` 任务应恢复入队，已发送任务不得重复。班级统一题日程补题后再降至仅剩一题时，只向教师发送公众号低库存提醒；低库存来源的 `skipped/dead` 任务也应能在对账时恢复。
5. 接收方查看私信或答疑消息、学生确认通知后，在 worker 发送前应将对应任务标记为 `skipped`；解绑、取消关注或停用接收账号也应跳过。答疑已读对学生和教师均按详情响应中的 `through_message_id` 截止，不能误标并发到达的新消息。
6. 网络、微信限频或 5xx 会有限重试；模板 ID、模板字段、AppSecret、IP 白名单或接口权限错误进入 `dead`。通过数据库只核对任务状态和脱敏错误码，不应看到正文、摘要、OpenID、access token 或微信响应正文。

私信和答疑正文会先去除首尾空白并把连续空白折叠为一个空格，再按 Unicode 字符保留前 40 个字符；超过时追加 `…`。通知不发送正文，只发送空白规范化后的主题。摘要、主题、发送人和事件时间均在 worker 实际发送前从源表即时读取，只存在于单次请求内存和发往微信的模板请求中；提醒任务表继续不保存这些字段。模板内容可能出现在微信消息列表或系统锁屏通知中，启用前应按实际隐私要求评估展示范围。

站内消息写入与提醒任务入队位于同一 PostgreSQL 事务，微信 HTTP 调用始终发生在提交后的 worker 中。worker 使用租约和 `FOR UPDATE SKIP LOCKED` 支持多实例接管，并在每次实际发送前以 owner 条件续租；等待期间租约已经过期或已被其他实例接管时不会继续调用微信。如果进程在微信接受消息后、写入 `sent` 前退出，模板消息接口缺少项目可控的幂等键，极端情况下仍可能重复发送一次提醒。`sent`、`skipped` 和 `dead` 任务保留 30 天，worker 每小时最多分 10 批清理 10000 条过期终态任务。

若后续从不含提醒入队代码的应用版本升级，不要让旧实例和已启用提醒的新实例长期混跑。应先执行包含提醒任务表的 forward migration，排空并停止旧实例流量，完成全部新实例部署后再统一设置 `WECHAT_MESSAGE_REMINDERS_ENABLED=true`；否则旧实例仍可提交站内消息，但它不具备提醒入队代码，无法事后自动回填对应任务。

管理员 `test-message` 仍使用客服消息接口和固定文本，受账号权限、频率及用户最近交互窗口约束。私信、通知和答疑业务提醒改用模板消息接口，不依赖管理员测试接口的客服窗口，但必须拥有对应模板消息权限，且模板 ID 和字段结构必须与同一 AppID 下的微信后台模板一致。测试号成功不代表正式账号天然具备相同权限。
