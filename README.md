# MathStudyPlatform

面向高等数学学习场景的多角色教学平台。前端采用 React，后端以 Go 服务作为唯一默认入口，覆盖认证、学习会话、智能练习、题库、班级、资源、外部账户绑定、管理后台和安全审计。

## 当前状态

- Go 后端已经完成默认运行链路切换，旧 Python 后端不再参与启动或部署。
- Tutor、Portrait、Diagnostician、Math Solver、Question Parser、Question Generator、OCR 七类 Eino Agent 已接入后台 provider/model/Agent 配置。
- 学生可在班级题目与 AI 自主练习之间切换，两类题目复用文本/图片判题、诊断和个人 DKT 更新链路。
- 纯图片答案支持 PNG、JPEG 和 GIF 上传、可信存储回读及多模态 OCR；文本与图片同时提交时以文本为准。OCR 或数学判定未得到可靠结果时不会写入作答、诊断、学习会话、DKT 或画像统计。
- 数学判定采用 `correct`、`incorrect`、`indeterminate` 三态；本地比较不能确定时可调用通用 Math Solver，解析生成结果还需独立验证答案和步骤，失败会返回可解释的降级原因。
- AI 自主出题在模型不可用或输出不合法时返回 `503 AI_GENERATION_UNAVAILABLE`，不会落库。

未完成工作统一记录在 [项目待办](docs/TODO.md)，README 不再维护重复路线图。

## 核心能力

| 场景 | 能力 |
|------|------|
| 学生 | 我的班级、班级题目、AI 自主练习、学习会话、错题本、知识图谱、学习路径、资源中心、消息中心与全站论坛、西电与公众号绑定 |
| 教师 | 班级与学生管理、题库管理、教学资源、学习数据、班级分析、消息中心与全站论坛、公众号绑定 |
| 管理员 | 用户管理、平台统计、全站论坛审核、AI provider/model/Agent 配置、知识图谱管理、系统设置和安全日志 |
| 平台 | JWT 与 Cookie 兼容认证、PostgreSQL/pgvector、Redis、对象存储、公众号模板消息提醒、Prometheus 指标和 Go forward migration |

## 快速开始

建议使用与仓库配置一致的 Go 1.25.13、Node.js 20、PostgreSQL 18 + pgvector 和 Redis 7。

```powershell
Copy-Item .env.example .env

Set-Location backend
go run ./cmd/migrate
go run ./cmd/api
```

另开终端启动前端：

```powershell
Set-Location frontend
npm install
npm run dev
```

Windows 也可以运行根目录的 `start.bat` 同时启动前后端。默认访问地址：

- 前端：http://localhost:5173
- Go API：http://localhost:8000
- 健康检查：http://localhost:8000/health

完整的环境配置、临时测试流程和部署步骤见下方技术文档。

## 版本号管理

正式版本遵循语义化版本，统一使用 `vMAJOR.MINOR.PATCH` 格式，例如 `v1.4.2`：

- `MAJOR`：包含不兼容的接口、数据或部署变更时递增。
- `MINOR`：以向后兼容方式增加功能时递增。
- `PATCH`：仅包含向后兼容的问题修复时递增。

正式发布通过 GitHub Actions 的 `Docker Release` 工作流手动触发，输入版本号是该次发布的唯一版本来源。当前工作流不接受预发布后缀，并为前后端镜像同时生成以下标签：

| 标签 | 用途 | 管理规则 |
|------|------|--------|
| `vMAJOR.MINOR.PATCH` | 正式发布、生产部署和回滚 | 不可覆盖 |
| `latest` | 指向最近一次成功的正式发布 | 可移动，不用于固定生产版本 |
| `sha-<短提交哈希>` | 按源码提交定位和排障 | 与该次构建的提交绑定 |

每次成功发布都会创建同版本号的 GitHub Release，发布说明自动列出前后端全部镜像标签、镜像 digest 和构建提交。GHCR 正式版本标签与 GitHub Release 标签必须保持一致；如果同版本镜像或 Release 已存在，必须递增版本号，不得覆盖或复用。生产部署应显式指定 `vMAJOR.MINOR.PATCH`，仅在临时验证时使用 `latest`，精确排障或回滚也可以使用 `sha-<提交哈希>`。具体发布、部署与回滚步骤见 [部署指南](docs/technical/deployment.md)。

## 技术文档

| 文档 | 内容 |
|------|------|
| [系统架构](docs/technical/architecture.md) | 技术栈、前后端分层、数据组件、AI/Agent 边界和目录结构 |
| [开发指南](docs/technical/development.md) | 本地环境、常用命令、临时测试、代码组织和数据库迁移 |
| [部署指南](docs/technical/deployment.md) | Docker Compose、环境变量、上线顺序、验证和回滚 |
| [文档索引](docs/README.md) | 当前文档、运维说明、迁移记录和历史归档入口 |
| [项目待办](docs/TODO.md) | 唯一的当前待办、优先级和验收标准 |

## 项目结构

```text
.
├── frontend/              # React + TypeScript 前端
├── backend/               # Go API、迁移和领域实现
├── docs/                  # 当前技术文档、待办和历史归档
├── scripts/               # 已有环境的生产升级脚本
├── docker-compose.yml     # PostgreSQL、Redis、Go API、前端编排
└── .env.example           # 唯一环境变量模板
```

## 开发约定

仓库开发与临时测试规则见 [AGENTS.md](AGENTS.md)。测试源码验证通过后必须删除且不得提交；数据库变更必须使用 Go forward migration，具体规则见 [迁移策略](backend/migrations/README.md)。
