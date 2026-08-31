# 部署指南

## 部署组成

根目录 `docker-compose.yml` 编排四个默认服务，并提供一个显式 `vector` profile 的 Qdrant 单节点：

| 服务 | 默认实现 | 容器端口 |
|------|----------|----------|
| PostgreSQL | `pgvector/pgvector:pg18-trixie` | 5432 |
| Redis | `redis:7-alpine` | 6379 |
| Backend | `backend/Dockerfile` 构建的 Go API | 8000 |
| Frontend | `frontend/Dockerfile` 构建的 Nginx 静态站点 | 80 |
| Qdrant（`vector` profile，可选） | `qdrant/qdrant:v1.14.1` | 6333 |

PostgreSQL、Redis、Go API、Qdrant 和前端 `9000` 端口默认只绑定宿主机回环地址；Qdrant profile 不会被默认 `docker compose up` 启动。

## 准备环境

```powershell
Copy-Item .env.example .env
```

使用 Compose 前必须在 `.env` 中显式设置每个环境唯一的随机 `POSTGRES_PASSWORD`；非开发环境会拒绝空值、占位值、与用户名相同或少于 16 字节的密码。生产环境还必须替换 `JWT_SECRET_KEY`、`FERNET_SECRET_KEY`、初始管理员密码、CORS 和管理网段。对象存储后端与云存储凭据不从 `.env` 读取，首次部署后由管理员在“系统设置 > 存储设置”中测试并保存；数据库中的 Access Key 和 Secret Key 使用 `FERNET_SECRET_KEY` 加密，因此该密钥必须稳定保存，不能在重启时轮换或留空。设置 `ENVIRONMENT=production`，不要把开发密钥或真实 `.env` 提交到仓库。启用公众号时还必须按消息模式设置 `WECHAT_OFFICIAL_ACCOUNT_*` 配置；`APP_SECRET`、`TOKEN` 和非明文模式使用的 `AES_KEY` 应由部署密钥系统或权限收紧的 `.env` 提供，不能写入镜像、Compose 文件或版本库。任何凭据出现在截图、日志或聊天记录中都视为泄露，应先在对应供应商控制台轮换再部署。

资源中心向量能力默认关闭。启用时设置 `QDRANT_ENABLED=true`、`QDRANT_URL`、`QDRANT_COLLECTION` 和独立的 `QDRANT_API_KEY`；生产环境没有 API key 时 API 会拒绝启动。Qdrant 只接受 adapter 发出的最小向量/payload，PostgreSQL 仍是资源、版本和权限真相。P1 不会因 Qdrant 运行时不可达而阻断 API，详细健康会显示 `degraded`；collection dimension/metric 必须由后续 embedding generation 显式校验。

对象存储运行配置遵循以下操作契约：

- `system_settings` 是对象存储后端与云存储凭据的唯一运行时来源；数据库尚无配置时 API 可以启动，但上传和图片回读保持不可用，直到管理员保存配置。
- `GET/PUT /api/v1/admin/settings/storage` 仅允许管理员访问；未保存时响应来源为 `unconfigured`，读取响应只返回凭据是否已配置，不返回明文或密文。
- `UPLOADS_DIR` 只定义本地文件系统的服务器根目录；它不选择存储后端，也不构成云存储配置回退。
- `POST /api/v1/admin/settings/storage/test` 使用当前草稿执行真实写入探测但不保存；保存操作也会先探测，失败时保留当前运行时后端。
- 探测固定覆盖 `documents/.mathstudy-storage-connectivity-check.txt`，内容不含凭据，不会随测试次数累积对象。
- 保存成功后新上传和 OCR 图片回读立即使用新后端；不会自动回退到旧后端，切换前应自行迁移仍需访问的历史对象。

安全日志保留任务默认随每个 Go API 实例启动并立即执行一次，之后每小时运行。`LOG_ARCHIVE_AFTER_DAYS` 和 `LOG_DELETE_AFTER_DAYS` 分别控制归档、删除期限，删除期限不得小于归档期限；`LOG_CLEANUP_ENABLED`、`LOG_CLEANUP_INTERVAL_SECONDS`、`LOG_CLEANUP_TIMEOUT_SECONDS` 控制开关、周期和单轮超时；`LOG_CLEANUP_BATCH_SIZE` 与 `LOG_CLEANUP_MAX_BATCHES` 将每个归档/删除阶段限制为有限批次。候选记录通过 PostgreSQL `FOR UPDATE SKIP LOCKED` 原子领取，因此滚动部署或多实例可同时启用，不需要指定单独调度实例。默认每批 500 条、每阶段最多 10 批；积压超过单轮上限时会由后续周期继续处理。`LOG_MAX_COUNT` 只用于管理端容量告警，不会改变保留期限。

每日一题默认由 Go API 进程在 `Asia/Shanghai` 零点预分配；进程启动或重启后也会立即补齐当天尚未处理的学生。`DAILY_QUESTION_PREGENERATION_ENABLED` 控制总开关，批大小、进程内并发、批间隔和单学生超时分别由 `DAILY_QUESTION_PREGENERATION_BATCH_SIZE`、`DAILY_QUESTION_PREGENERATION_CONCURRENCY`、`DAILY_QUESTION_PREGENERATION_BATCH_INTERVAL_MS`、`DAILY_QUESTION_PREGENERATION_STUDENT_TIMEOUT_SECONDS` 控制；单学生超时必须小于 120 秒，避免超过准备行的失效接管窗口。`preparing` 及 AI、Solver、限频和瞬态仓储失败会在当天约 5 分钟后幂等重扫，持久化 `retry_count` 最多允许三次后台重领；未布置统一题不会持久化失败 assignment，因此 worker 会在当天继续低成本重扫，并在教师补排期后自动补分配；无目标知识点和 AI 权限/配额阻断等配置错误不循环。学生进入每日题页面后的手动恢复不受后台次数上限限制。多实例部署仍应只在一个调度实例设置 `DAILY_QUESTION_PREGENERATION_ENABLED=true`，其余实例设为 `false`；assignment 唯一约束和 generation token 防止同一学生重复落题，但不替代全局 worker 租约。

开启 `WECHAT_MESSAGE_REMINDERS_ENABLED=true` 后，教师可以按班级开启每日一题自动提醒。API 进程会在上海时间每天 08:00 扫描已开启的班级；进程在 08:00 后启动时也会补做当日幂等扫描，单次数据库故障会在当天每 5 分钟重试。仅有可作答且未完成的每日题时，才创建公众号模板消息任务；不会创建 `notices` 或站内通知。教师当天把自动提醒从关闭改为开启时会立即尝试入队，策略与开关的并发保存按字段合并；若当天已有手动提醒，则首次自动扫描不再重复提醒。自动提醒按班级和日期保持唯一，对账会补充缺失学生并恢复 `skipped/dead`，但不重置已发送任务。教师每次手动点击都会创建独立事件，并为点击时仍未完成的学生创建新一轮公众号任务；连续或并发点击不会复用上一轮来源，也不需要等待上一轮进入终态。班级统一题日程剩余恰好一道时只提醒教师；低库存对账按发送时的上海自然日核对，并可恢复 `skipped/dead`。已发送的阈值事件不会因 30 天任务清理而重复，补题后再次降至一道才生成新提醒。发送前仍会校验当前题目、完成状态、绑定和关注状态。每日题提醒复用 `WECHAT_NOTICE_TEMPLATE_ID`，不增加新的公众号模板或环境变量。

公众号配置项如下：

| 配置 | 用途 |
| --- | --- |
| `WECHAT_OFFICIAL_ACCOUNT_ENABLED` | 是否启用回调、师生绑定和管理员测试消息；默认 `false` |
| `WECHAT_OFFICIAL_ACCOUNT_APP_ID` | 公众号或测试号 AppID |
| `WECHAT_OFFICIAL_ACCOUNT_APP_SECRET` | 调用微信 API 的密钥 |
| `WECHAT_OFFICIAL_ACCOUNT_TOKEN` | 回调签名共享 Token，必须为 3 至 32 位 ASCII 字母或数字 |
| `WECHAT_OFFICIAL_ACCOUNT_AES_KEY` | 微信后台生成的 43 字符 `EncodingAESKey`；兼容或安全模式必填 |
| `WECHAT_OFFICIAL_ACCOUNT_MESSAGE_MODE` | `plain`、`compatible` 或 `safe` |
| `WECHAT_OFFICIAL_ACCOUNT_NAME` | 前端展示的公众号名称 |
| `WECHAT_OFFICIAL_ACCOUNT_HTTP_TIMEOUT_SECONDS` | 后端调用微信 API 的单次 HTTP 请求超时，范围 1 至 60 秒，默认 10 秒 |
| `WECHAT_MESSAGE_REMINDERS_ENABLED` | 是否为私信、班级通知、答疑和每日一题启用微信公众号模板提醒；默认 `false`，启用时要求公众号总开关已开启 |
| `WECHAT_PRIVATE_MESSAGE_TEMPLATE_ID` | 私信提醒模板 ID；提醒总开关开启时必填 |
| `WECHAT_NOTICE_TEMPLATE_ID` | 班级通知提醒模板 ID；提醒总开关开启时必填 |
| `WECHAT_QA_MESSAGE_TEMPLATE_ID` | 答疑提醒模板 ID；提醒总开关开启时必填 |

消息模式必须与微信后台一致：`plain` 对应明文模式且只接收明文；`compatible` 对应兼容模式并同时接收明文和 AES 消息；`safe` 对应安全模式且只接收 AES 消息。兼容和安全模式必须配置同一公众号生成的 `EncodingAESKey`。下文回调路径使用默认 `API_V1_PREFIX=/api/v1`；部署若修改该前缀，微信后台 URL 和所有调试 API 路径必须同步替换。

## 构建与启动

GitHub Actions 将两个应用服务作为独立镜像构建，避免混用 Dockerfile 或构建上下文：

| 服务 | Dockerfile | 构建上下文 | GHCR 镜像 |
|------|------------|------------|-----------|
| Backend | `backend/Dockerfile` | `backend/` | `ghcr.io/fraternity-z/mathstudyplatform-backend` |
| Frontend | `frontend/Dockerfile` | `frontend/` | `ghcr.io/fraternity-z/mathstudyplatform-frontend` |

`.github/workflows/docker-build-check.yml` 在 push、Pull Request 和手动触发时只验证镜像构建，不登录或推送仓库。`.github/workflows/docker-release.yml` 仅手动触发，从 Actions 页面选择的 Git ref 构建并发布版本号、`latest` 和短提交哈希标签；正式版本号必须使用 `v1.0.0` 格式，任一服务已存在同版本标签或同名 GitHub Release 时发布会终止，避免覆盖。镜像推送成功后，工作流还会创建同版本的正式 GitHub Release，发布说明自动列出前后端全部镜像标签、镜像 digest 和构建提交；Docker Build 的详细构建记录仍保留在对应的 Actions 运行摘要中。

```powershell
docker compose build
docker compose up -d postgres redis
```

开发或集成环境需要 Qdrant 时单独启用 profile：

```powershell
docker compose --profile vector up -d qdrant
docker compose --profile vector ps qdrant
```

宿主机运行 Go API 使用 `QDRANT_URL=http://localhost:6333`；后端也在 Compose 内运行时使用 `QDRANT_URL=http://qdrant:6333`。停止 Qdrant profile 不会删除 PostgreSQL 数据或资源对象。

数据库健康后执行 Go migration runner，再启动应用服务。仓库仅保留 `scripts/update.sh` 作为已有环境升级入口，不提供首次生产部署脚本。首次部署由运维人员在完成 `.env` 和边缘代理配置后按下述顺序执行；私有 GHCR 包应先使用最小权限凭据登录，Token 不写入配置或日志。

```bash
export IMAGE_VERSION=v1.0.0
docker compose pull backend frontend
docker compose up -d postgres redis
# 确认 PostgreSQL 健康后继续
docker compose exec -T postgres sh -ec 'psql -v ON_ERROR_STOP=1 -U "${POSTGRES_USER:-postgres}" -d "${POSTGRES_DB:-${POSTGRES_USER:-postgres}}" -c "CREATE EXTENSION IF NOT EXISTS vector"'
docker compose run --rm --no-deps backend msp-migrate
docker compose up -d backend frontend

# 已有环境升级
sudo bash ./scripts/update.sh --version v1.1.0
```

更新脚本要求完整的现有部署，会在停止应用写入后备份 PostgreSQL、`.env`、解析后的 Compose 配置、旧镜像 ID 和本地上传目录。首次部署的手工流程与更新脚本都在迁移前幂等启用 `vector` 扩展，因此 Compose 不依赖独立的数据库初始化脚本。从源码手工部署时可使用：

```powershell
Set-Location backend
go run ./cmd/migrate
Set-Location ..
docker compose up -d backend frontend
```

默认生产链路不运行 Python 或 Alembic。

迁移 runner 不随 API 自动执行。合并包含 `backend/migrations/*.up.sql` 的变更后，必须在启动新版本 API 前执行一次迁移，并确认重复执行没有待应用版本。已有环境不得在旧 API 仍接收写请求时直接执行迁移，应使用更新脚本先停止应用、完成备份，再迁移并启动新版本：

```powershell
Set-Location backend
go run ./cmd/migrate
go run ./cmd/migrate
Set-Location ..
```

首次命令应记录实际 `applied_count`，第二次应为 `applied_count=0`。也可在数据库中核对已应用版本：

```sql
SELECT version, name, applied_at
FROM public.go_schema_migrations
ORDER BY version;
```

当前迁移链由 `0001` 至 `0017` 十七个迁移组成。`0005` 至 `0010` 交付每日题、画像、每日题一致性和错题闭环；`0011` 至 `0014` 交付论坛、学习会话模式、首次聊天幂等和 AI 参数默认值；`0015_auth_version` 为账户增加认证版本，使密码和状态变化立即撤销既有令牌；`0016_local_upload_access` 记录本地对象上传者，并为附件和公开资源的对象级授权查询增加索引；`0017_resource_vector_foundation` 交付资源中心租户/知识库、文档版本/chunk、模型版本、generation、job 和可靠 outbox 基础。

全新空库第一次 migration runner 应记录版本 `1` 至 `17`；version 16 数据库只应用 version 17，紧接着重复执行应为 `applied_count=0`。执行任何待应用迁移前都应停止应用写入、完成可恢复备份，并在维护窗口迁移；`0015` 上线时新 API 必须在迁移成功后一次性切换，不能与仍签发无 `auth_version` 令牌的旧实例混跑。迁移完成后所有旧会话都会失效，用户需要重新登录。

迁移后应确认版本 17 已记录、`users.auth_version` 非空且至少为 1，并确认 `tenants`、默认 `knowledge_bases`、`resource_documents`、`document_versions`、`document_chunks`、`embedding_model_versions`、`vector_index_generations`、`resource_processing_jobs` 及 outbox lease 字段均已生效。迁移不会根据客户端可写的历史引用自动认领对象；缺少可信所有权记录的既有本地文件按设计返回 `404`，只能重新上传或通过单独评审、可审计的数据导入建立所有权，不得通过直接暴露上传目录规避。曾完整执行未发布草稿 version 10 至 13 的本地数据库已经具备部分最终结构，但账本与当前迁移链可能不兼容；禁止删除版本记录后盲目重放，必须先备份、核对最终结构，再按迁移 README 中经过评审的专用校准流程处理。

重整前执行过旧开发迁移链的数据库（该旧链也曾占用 `0001` 至 `0015`，但迁移名称和内容不同）不属于可原地升级目标。migration runner 会校验迁移版本、名称和未知记录，并在账本与当前代码不一致时拒绝继续。可丢弃的开发库应删除并重建；任何不可丢弃的库必须先停止发布，完成实际 schema、业务数据和 `go_schema_migrations` 核对，再设计专门的数据保留迁移，禁止删除版本记录后重放基线。

若后续从不含提醒入队代码的旧应用升级，必须先应用包含提醒任务表的 forward migration，再排空并停止旧实例流量，完成所有新实例部署后统一开启 `WECHAT_MESSAGE_REMINDERS_ENABLED=true`。不能在旧实例仍接收写请求时直接混合启用：旧实例可以提交私信、通知或答疑，但不会生成提醒任务，且系统不从正文表回填历史任务。

## 反向代理

`frontend/nginx.conf` 负责前端容器内的静态资源和 API 转发。站点级边缘代理由各部署环境独立管理，不从仓库示例配置覆盖。部署时应确认：

默认请求超时按工作负载分层：

| 层级 | 默认超时 |
| --- | ---: |
| 普通 Go API 总请求预算 | 30 秒 |
| 练习、每日题准备和画像 AI 接口总请求预算 | 55 秒 |
| AI 学习会话聊天总请求预算 | 130 秒 |
| OpenAI Responses 请求总预算 | 130 秒 |
| 前端生成请求 Axios 超时 | 60 秒 |
| Nginx `/api/` 上游响应读取超时 | 300 秒 |
| Go HTTP `WriteTimeout` | 310 秒 |

`EXERCISE_GENERATION_REQUEST_TIMEOUT_SECONDS` 只用于 `/exercise/generate`、`/daily-question/today/prepare` 和 `/portrait/generate`，默认 55 秒。`SESSION_CHAT_REQUEST_TIMEOUT_SECONDS` 独立用于精确的 `POST /session/start-chat`、`POST /session/{session_id}/chat` 和 `POST /v1/responses`，默认 130 秒；该预算覆盖最长 60 秒内容审核、最长 60 秒模型调用，并为降级回复或收尾处理保留余量。会话聊天和 Responses 流都使用端到端 SSE，不受前端生成请求的 60 秒 Axios 超时约束；已有更短的父级 context 仍会优先结束请求。每个分片都会刷新响应，因此所有代理层都必须禁用响应缓冲和压缩聚合，不能只延长读取超时。若将任一后端预算配置到 300 秒以上，必须同步调整所有边缘代理和 Go HTTP 写入超时。Nginx 的 300 秒仅是代理安全上限，不代表业务请求应持续运行 300 秒。

- `/api/` 指向 Go API；
- `/v1/` 指向 Go API，至少开放 `POST /v1/responses`；必须原样保留 `Authorization`，并关闭响应缓冲、请求缓冲和 gzip，以支持 Responses SSE；
- `/uploads/` 指向 Go API，并保留浏览器 Cookie；该入口承载受认证的本地上传读取，不能由边缘代理或 CDN 绕过后端直接暴露宿主机 `uploads/` 目录；
- 微信回调 `GET/POST /api/v1/integrations/wechat/official-account/callback` 必须通过公网 HTTPS 原样转发到 Go API，不能要求站内 JWT；该路由使用微信签名和时间戳校验请求；
- SSE 路径关闭代理缓冲和压缩聚合、允许逐分片 flush，并保留足够超时；
- 上传大小限制与后端配置一致；
- TLS、HSTS、CSP 和其他安全响应头由边缘代理统一设置；
- `/metrics` 和详细健康信息只对管理网络开放。

Compose 中 frontend 的 `9000` 端口仅绑定宿主机 `127.0.0.1`，用于同机边缘代理回源和本机验收。禁止把该绑定改回所有网卡后直接作为公网入口；公网流量必须经配置 TLS 的边缘代理进入，远程边缘代理应改用受限私网或容器网络回源。

首次部署时需由运维人员在实际的边缘代理中配置站点；`scripts/update.sh` 不修改 Nginx。已经部署的服务器不会因代码更新自动改写 `/etc/nginx`；应先用 `sudo nginx -T` 确认实际生效的站点文件，再将 `/api/` location 中的 `proxy_read_timeout` 调整为 `300s`，随后执行：

```bash
sudo nginx -t
sudo systemctl reload nginx
```

不要在通用更新脚本中自动编辑系统 Nginx；自定义域名、面板托管、Ingress、云负载均衡或其他边缘代理应在各自配置入口应用相同的响应读取上限。

## 监控指标

`GET /metrics` 使用 Prometheus text exposition，并保留既有无标签总计 `msp_http_requests_total`。新增指标包括：

- `msp_http_server_requests_total{method,route,status_class}`：按 HTTP 方法、ServeMux 路由模板和状态类别统计请求量。
- `msp_http_server_request_duration_seconds`：使用相同低基数标签的请求时延直方图。
- `msp_postgres_pool_*`：pgx 连接上限、当前 total/acquired/idle/constructing、获取次数、等待和取消。
- `msp_redis_pool_*`：go-redis 当前连接、连接复用命中/未命中、等待、超时和不可用连接。
- `msp_openai_responses_input_tokens_total`：Responses 终态中 provider 报告的输入 token 累计值。
- `msp_openai_responses_output_tokens_total`：Responses 终态中 provider 报告的输出 token 累计值。

`route` 只使用注册路由模板；未匹配请求和 CORS preflight 使用固定占位符。不要把原始 URL、用户 ID、request ID 或错误文本加入 label。常用查询示例：

```promql
# 各路由 5 分钟 P95
histogram_quantile(
  0.95,
  sum by (le, method, route) (
    rate(msp_http_server_request_duration_seconds_bucket[5m])
  )
)

# PostgreSQL 连接池占用率
msp_postgres_pool_connections{state="acquired"}
  / msp_postgres_pool_max_connections
```

部署告警至少应覆盖 HTTP 5xx 比例、核心路由 P95/P99、PostgreSQL canceled/empty acquire 增长，以及 Redis pool timeout/wait 增长。

## 上线验证

```powershell
docker compose ps
docker compose logs --tail 200 backend
```

至少验证：

1. `/health` 返回成功，数据库和 Redis 容器健康。
2. 前端页面可以加载并调用 `/api/v1`。
3. 登录和刷新令牌符合预期；停用账户、修改或重置密码后，旧 access/refresh token 均返回 `401`，当前浏览器改密后回到登录页。

4. 数据库迁移首次执行有新增版本，重复执行无待应用版本。
5. 启用公众号时，在微信后台将回调 URL 配置为 `https://<public-host>/api/v1/integrations/wechat/official-account/callback`，确认 GET 验证、关注/取关事件和文本绑定回调均成功。
6. 分别使用测试学生和测试教师生成绑定口令并完成绑定，确认两端个人中心均显示已绑定/已关注；随后由管理员调用 `POST /api/v1/admin/wechat/test-message` 向两类账号发送服务端固定测试内容。
7. 为私信、通知和答疑配置字段均为 `keyword1`、`keyword2`、`keyword3` 的模板及对应 ID，开启 `WECHAT_MESSAGE_REMINDERS_ENABLED=true` 后依次验证学生发教师、教师发学生的私信，教师班级通知，以及学生发起、师生回复的答疑。私信和答疑只展示空白规范化后的前 40 个 Unicode 字符并在截断时追加 `…`；通知只展示主题。再验证已读、通知已确认、解绑、取关和账号停用时任务转为 `skipped`。随后为测试班级开启每日一题自动提醒，验证 08:00 扫描、当天即时补发、无题跳过、手动提醒和统一题仅剩一题的教师预警均只生成公众号任务，不写入站内通知。
8. 在管理员“存储设置”中分别执行目标后端的连接测试和保存，确认无需重启即可完成一次上传；外部 AI provider 和西电账户绑定也按部署配置进行连通性验证，`ocr` Agent 必须选择支持图片输入的模型。
9. 分别提交真实 PNG、JPEG 图片和空白/低对比图片，确认成功路径只产生一次 attempt，并各执行一次 session、DKT 和 profile 更新；OCR/数学不确定或失败路径的这些写入均为零。图片 OCR 当前只接受 PNG、JPEG 和 GIF。
10. 验证通用数学判定的 `correct`、`incorrect`、`indeterminate` 响应，以及解析生成不可用、超时、取消、无效输出和验证失败的 `failure.stage`、`failure.code`、`retryable` 契约。
11. 对两条学习会话聊天接口各发起一次真实 Tutor 请求，确认 `session_info`（仅首次聊天）或 `task_info` 可建立接收状态，缺少元信息时首个 chunk 或 done 仍能兜底确认；模型完成前收到至少两个 `message` chunk，正常路径最终只收到一次 done 且历史记录内容与分片拼接一致。生成期间应能编辑下一轮文字草稿，但发送、回车、附件、语音、模式切换和本轮操作均锁定。分别在零分片和已有分片后停止，确认原 SSE 消费 `cancelled` 后才结算，历史只保留一个助手消息并以精确独立行 `> 已停止生成` 结尾；并发或稍晚重复调用取消接口应得到相同结果，取消成功后立即发送下一轮不应误触发旧 AI 并发租约。再模拟先发生的客户端断连、provider、网络、超时或流传输异常，确认随后取消不会把它改写成用户停止，部分回复以 `> 生成已中断` 结尾。另模拟首轮进程中断留下已过期 claim 且没有助手回复，确认下一次普通聊天先补写中断回复再处理新问题；原首轮恰好并发完成时必须读取完成态，不得误报不可恢复。以上路径均不得重放或提供“重试上一轮”，下一轮草稿保持不变。并发请求应复用上游连接，流已经输出后发生 provider 错误时不得切换候选模型拼接第二份回复。任务取消注册表及 5 分钟停止 tombstone 仅存在于处理流的 API 进程内，所以该验收使用单 API 实例；多实例上线前必须补粘性路由或共享取消协调并重新验收。
12. 使用平台学生 access token 对 `/v1/responses` 分别发送非流式和 `stream=true` 请求，确认逻辑模型映射、响应 ID/模型/usage、具名 SSE 文本和函数调用事件、终态事件、请求取消释放、学生日额度及流开始后不跨渠道重试；再以 `store=true`、不支持的工具和不存在模型确认稳定 OpenAI 错误结构。

公众号 live 验收必须记录实际账号类型、认证状态、接口权限、IP 白名单、模板字段和微信返回码。管理员固定测试消息仍走客服消息接口并受用户最近交互窗口约束；三类业务提醒走模板消息接口，必须单独验收目标账号的模板权限、模板 ID 和字段结构。测试号链路通过只能证明代码与测试环境可用，不能替代正式公众号的权限验收，也不能保证可以任意主动群发。

业务消息先与提醒任务在同一 PostgreSQL 事务提交，再由 API 进程内 worker 在事务外调用微信。多实例通过 `FOR UPDATE SKIP LOCKED` 和 owner 租约协调；发送前重新检查接收账号、当前 AppID 绑定、关注状态和未读/未确认状态，并从业务原表读取模板字段，之后在调用微信前续租，已经失效或被接管的 owner 不再发送。瞬时网络、限频和 5xx 错误有限重试；解绑、取关、已读或已确认不重试；模板、永久配置或权限错误进入 `dead` 并写脱敏错误码。任务表不保存正文、摘要、主题、OpenID、AppSecret、access token 或微信原始响应。模板摘要会发送给微信并可能显示在锁屏通知中，生产启用前必须完成隐私评估。终态任务保留 30 天，worker 每小时分批清理，每轮最多删除 10000 条；迁移包含终态清理和收件人外键所需索引。

多实例部署必须共享 Redis。回调处理采用 6 秒 owner 租约，成功后转为 24 小时完成态并保存被动回复；处理中重试返回 503，完成态重试重放回复。绑定口令按微信事件 ID 预留并保留原 TTL，避免进程在 Redis 消费后、PostgreSQL 落库前退出时丢失绑定。关注状态只接受不早于当前状态的微信 `CreateTime`，同秒冲突由取关优先。稳定版 access token 刷新锁按单次微信 HTTP 请求超时再加 10 秒余量计算，等待锁和实际刷新使用独立预算；配置上限为 60 秒。

仓库不永久保留验收测试源码。发布前按 [开发指南](development.md) 临时创建非网络验收用例，覆盖真实 PNG/JPEG 的上传、存储回读、多模态 Base64 传递和学习状态写入边界，运行并记录结果后删除：

```powershell
Set-Location backend
go test ./internal/adapter/llm/einoagent -run 'TestAnswerImageSubmission' -count=1 -v
```

发布环境还应使用目标视觉 provider 执行 live OCR 质量验收。临时用例包含 `x+1`、`42`、空白 PNG 和低对比 JPEG；凭据只通过环境变量提供，不写入仓库，用例通过后立即删除：

```powershell
$env:MSP_LIVE_OCR_ACCEPTANCE = '1'
$env:MSP_OCR_ACCEPTANCE_BASE_URL = 'https://provider.example.com/v1'
$env:MSP_OCR_ACCEPTANCE_API_KEY = '<secret>'
$env:MSP_OCR_ACCEPTANCE_MODEL = '<vision-model>'
go test ./internal/adapter/llm/einoagent -run 'TestLiveAnswerOCR' -count=1 -v
```

目标 Math Solver provider 的通用题型质量验收使用独立开关和临时用例，覆盖三角恒等、极限、不定积分、方程解集、矩阵、证明和错误步骤拒绝；记录结果后删除用例源码：

```powershell
$env:MSP_LIVE_MATH_ACCEPTANCE = '1'
$env:MSP_MATH_ACCEPTANCE_BASE_URL = 'https://provider.example.com/v1'
$env:MSP_MATH_ACCEPTANCE_API_KEY = '<secret>'
$env:MSP_MATH_ACCEPTANCE_MODEL = '<math-model>'
go test ./internal/adapter/llm/einoagent -run 'TestLiveMathSolver' -count=1 -v
```

登录安全验证使用 Redis 保存短时一次性票据。生产环境必须保持 Redis 可用，并可通过以下环境变量调整策略：

- `LOGIN_CAPTCHA_TTL_SECONDS`：拼图挑战有效期，默认 120 秒。
- `LOGIN_CAPTCHA_PROOF_TTL_SECONDS`：验证通过后登录票据有效期，默认 120 秒。
- `LOGIN_CAPTCHA_TOLERANCE_PIXELS`：拼图位置容差，默认 6 像素。
- `LOGIN_CAPTCHA_ISSUE_LIMIT`：单客户端在窗口内最多签发数量，默认 10。
- `LOGIN_CAPTCHA_ISSUE_WINDOW_SECONDS`：签发限频窗口，默认 60 秒。

反向代理需要覆盖写入 `X-Real-IP`；仓库内 Nginx 配置已包含该请求头。验证码图片和校验响应均禁止缓存。

尚未完成的运行时验收范围记录在 [项目待办](../TODO.md)。

## 完整备份与脱敏数据交换

管理员“数据库”页面提供的是脱敏 JSON 数据交换，不是数据库备份或恢复工具。导出内容不包含用户账号表、管理员账号、密码和其他敏感字段，也不覆盖全部业务表；导入要求目标库已经存在所需账号和关联基础数据，不能用于空库完整恢复。旧版脱敏 JSON 中只要包含非空 `users` 数据，导入会在写库前明确拒绝，且不会生成、填充或绕过密码字段。

完整备份必须使用更新流程生成的 PostgreSQL custom-format archive。`scripts/update.sh` 通过 `pg_dump --format=custom --no-owner --no-privileges` 创建 `postgres.dump`；恢复前先用 `pg_restore --list` 校验 archive，再按下方流程停止应用、清理目标 schema 并执行 `pg_restore --exit-on-error`。管理员 JSON 文件不能替代该流程。

## 更新与回滚

- 使用 `scripts/update.sh --version <镜像标签>` 或按“确认数据库、拉取镜像、停止应用写入、备份数据、迁移、启动新应用”的顺序更新。脚本确认 PostgreSQL 可用并拉取新镜像时旧应用仍保持运行，随后只停止 backend/frontend，不停止数据库和 Redis。
- 更新脚本在迁移前创建权限收紧的 `backups/<时间戳>/`，保存 `.env`、解析后的 Compose 配置、旧镜像引用及不可变镜像 ID、PostgreSQL custom-format dump，以及存在时的 `uploads.tar.gz`。可通过 `BACKUP_ROOT` 修改备份根目录。该目录被 Git 忽略，但包含生产凭据和业务数据，仍需限制访问并按运维保留策略清理。
- `postgres.dump` 失败或为空时脚本不会执行迁移，并尝试重新启动原应用容器；迁移失败时应用保持停止，避免在未知 schema 状态下继续提供服务。目标版本启动后脚本不再以容器健康状态作为停服门禁，更新结果通过 `docker compose ps` 展示，并应继续执行认证和核心业务 smoke。
- `uploads/` 不在默认路径时，通过 `MSP_UPLOADS_BACKUP_DIR` 指定宿主机持久化目录。使用 S3/七牛等外部对象存储时，仍需遵循对应供应商的版本与备份策略。
- 数据迁移不提供自动 down migration；失败时恢复备份，或发布经过评审的补偿性 forward migration。
- 应用镜像回滚前必须确认旧版本能够读取当前数据库结构。
- 回滚后重新执行健康检查、认证和核心业务 smoke。

需要恢复备份时，先停止应用并保留故障现场，再使用对应备份目录。以下命令会覆盖当前数据库与本地上传目录，只能在确认目标目录和恢复窗口后执行：

```bash
COMPOSE_FILE="${COMPOSE_FILE:-docker-compose.yml}"
BACKUP_ROOT="${BACKUP_ROOT:-backups}"
BACKUP_DIR="${BACKUP_ROOT}/20260723_120000"
docker compose -f "${COMPOSE_FILE}" stop backend frontend
docker compose -f "${COMPOSE_FILE}" up -d postgres
for attempt in {1..30}; do
    if docker compose -f "${COMPOSE_FILE}" exec -T postgres sh -ec 'pg_isready -q -U "${POSTGRES_USER:-postgres}" -d "${POSTGRES_DB:-${POSTGRES_USER:-postgres}}"'; then
        break
    fi
    if [ "$attempt" -eq 30 ]; then
        echo "PostgreSQL 未在 60 秒内就绪" >&2
        exit 1
    fi
    sleep 2
done

# 先验证 archive，再清空项目 schema；仅使用 --clean 无法删除备份后新增的表。
docker compose -f "${COMPOSE_FILE}" exec -T postgres sh -ec 'exec pg_restore --list >/dev/null' < "${BACKUP_DIR}/postgres.dump"
docker compose -f "${COMPOSE_FILE}" exec -T postgres sh -ec 'exec psql -v ON_ERROR_STOP=1 -U "${POSTGRES_USER:-postgres}" -d "${POSTGRES_DB:-${POSTGRES_USER:-postgres}}" -c "DROP SCHEMA public CASCADE"'
docker compose -f "${COMPOSE_FILE}" exec -T postgres sh -ec 'exec pg_restore --exit-on-error --clean --if-exists --no-owner --no-privileges -U "${POSTGRES_USER:-postgres}" -d "${POSTGRES_DB:-${POSTGRES_USER:-postgres}}"' < "${BACKUP_DIR}/postgres.dump"

# 仅当备份中存在 uploads.tar.gz 且使用本地存储时执行。
UPLOADS_DIR="${MSP_UPLOADS_BACKUP_DIR:-uploads}"
if [ -f "${BACKUP_DIR}/uploads.tar.gz" ]; then
    if [ -e "${UPLOADS_DIR}" ]; then
        mv "${UPLOADS_DIR}" "${UPLOADS_DIR}.failed-$(date +%Y%m%d_%H%M%S)"
    fi
    mkdir -p "$(dirname -- "${UPLOADS_DIR}")"
    tar -xzf "${BACKUP_DIR}/uploads.tar.gz" -C "$(dirname -- "${UPLOADS_DIR}")"
elif [ -f "${BACKUP_DIR}/uploads.absent.txt" ]; then
    if [ -e "${UPLOADS_DIR}" ]; then
        mv "${UPLOADS_DIR}" "${UPLOADS_DIR}.failed-$(date +%Y%m%d_%H%M%S)"
    fi
    echo "备份时上传目录不存在，当前目录已保留为故障现场"
fi
```

`previous-images.txt` 同时保存原镜像引用和容器实际使用的镜像 ID。需要回滚镜像时，先确认两个 ID 在本机仍存在，再把它们标记为同一个临时版本并启动应用：

```bash
BACKEND_IMAGE="${BACKEND_IMAGE:-ghcr.io/fraternity-z/mathstudyplatform-backend}"
FRONTEND_IMAGE="${FRONTEND_IMAGE:-ghcr.io/fraternity-z/mathstudyplatform-frontend}"
ROLLBACK_TAG="rollback-20260723_120000"
BACKEND_IMAGE_ID="$(sed -n 's/^backend_image_id=//p' "${BACKUP_DIR}/previous-images.txt")"
FRONTEND_IMAGE_ID="$(sed -n 's/^frontend_image_id=//p' "${BACKUP_DIR}/previous-images.txt")"
docker image inspect "${BACKEND_IMAGE_ID}" "${FRONTEND_IMAGE_ID}" >/dev/null
docker tag "${BACKEND_IMAGE_ID}" "${BACKEND_IMAGE}:${ROLLBACK_TAG}"
docker tag "${FRONTEND_IMAGE_ID}" "${FRONTEND_IMAGE}:${ROLLBACK_TAG}"
export BACKEND_IMAGE FRONTEND_IMAGE
export IMAGE_VERSION="${ROLLBACK_TAG}"
docker compose -f "${COMPOSE_FILE}" up -d backend frontend
```

恢复备份中的 `.env` 后再使用上述旧镜像。数据库已成功完成 forward migration、且旧镜像确认兼容新 schema 时，可以只回滚镜像；否则必须先恢复数据库或发布补偿迁移，不能只复制旧 `.env` 后直接启动。
