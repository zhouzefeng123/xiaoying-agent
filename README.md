# xiaoying-agent

小英智能体帮你解决一切
# XiaoYing Agent

小英智能体是一个面向个人和团队的 AI 工作助手，提供桌面端、网页端和 Go 后端服务。它不仅能进行多模型对话，还可以调用本地工具完成文件处理、代码编辑、终端命令、文档生成、任务调度和多智能体协作。

项目采用前后端分离架构：

- `xiaoying-agent-web`：React 19 + TypeScript + Vite + Electron 客户端。
- `xiaoying-agent-api`：Go 后端，提供 Agent、模型接入、工具调用、记忆、数据库和 WebSocket 服务。
- `deploy`：基于 Docker Compose 的 Web 部署方案，包含 Nginx、Go API 和 MySQL。

## 主要功能

- **多模型支持**：可接入 OpenAI、DeepSeek、Anthropic、Gemini、阿里云百炼、Groq、OpenRouter、Azure OpenAI 等模型服务。
- **智能体工具调用**：支持读写文件、代码编辑、执行命令、网页抓取、代码检索和诊断。
- **会话与长期记忆**：支持会话历史、上下文压缩、知识库、记忆检索和认知状态管理。
- **多智能体协作**：支持任务拆分、子 Agent、并行任务和结果汇总。
- **桌面与网页双端**：可作为 Electron 桌面应用运行，也可通过浏览器访问。
- **文档与办公能力**：内置 Word 文档编辑、PPT、Excel、PDF 等技能。
- **语音交互**：支持语音识别、TTS 和唤醒词等能力。
- **MCP 与 LSP**：可连接 MCP 工具，并通过 LSP 获取代码诊断信息。
- **定时任务**：支持后台任务、消息提醒和自动执行。
- **Docker 部署**：一条命令启动完整的 Web 服务。

## 技术栈

| 模块 | 技术 |
| --- | --- |
| 前端 | React 19、TypeScript、Vite、Electron |
| UI 与编辑 | Tailwind CSS、Monaco Editor、xterm.js |
| 后端 | Go 1.25、Cobra、WebSocket |
| 数据库 | MySQL 8.0；支持从旧版 SQLite 导入数据 |
| 部署 | Docker Compose、Nginx |
| AI 能力 | 多 LLM Provider、MCP、LSP、Embedding |

## 项目结构

```text
xiaoying-agent/
├── deploy/                    # Docker Compose 生产部署
│   ├── .env.example           # 部署环境变量模板
│   ├── docker-compose.yml
│   └── nginx.conf
├── xiaoying-agent-api/        # Go 后端
│   ├── cmd/                   # 服务启动和迁移命令
│   ├── internal/              # Agent、模型、数据库、工具等模块
│   ├── system_skills/         # 内置系统技能
│   └── main.go
├── xiaoying-agent-web/        # React / Electron 前端
│   ├── electron/              # Electron 主进程与预加载脚本
│   ├── src/                   # React 页面和业务组件
│   └── vite.config.ts
├── README.md
└── .gitignore
```

## 环境要求

### Docker 启动

- Docker 24 或更高版本
- Docker Compose v2

### 本地开发

- Node.js 22 或更高版本
- npm 10 或更高版本
- Go 1.25 或更高版本
- MySQL 8.0
- Windows、macOS 或 Linux

Electron 桌面应用目前主要在 Windows 环境下开发和打包。

## 快速启动

### 方式一：Docker 一键启动（推荐）

这种方式会同时启动 MySQL、Go API 和 Nginx Web 页面。

1. 克隆项目：

```bash
git clone https://github.com/zhouzefeng123/xiaoying-agent.git
cd xiaoying-agent
```

2. 准备配置文件：

```powershell
New-Item -ItemType Directory -Force .\deploy\config
Copy-Item .\deploy\.env.example .\deploy\.env
Copy-Item .\xiaoying-agent-api\.run\auramate.with-db.json .\deploy\config\.auramate.json
New-Item -ItemType Directory -Force .\deploy\data\workspace
New-Item -ItemType Directory -Force .\deploy\data\database
New-Item -ItemType Directory -Force .\deploy\data\logs
```

如果使用 macOS 或 Linux：

```bash
mkdir -p deploy/config deploy/data/workspace deploy/data/database deploy/data/logs
cp deploy/.env.example deploy/.env
cp xiaoying-agent-api/.run/auramate.with-db.json deploy/config/.auramate.json
```

3. 编辑 `deploy/.env`，至少修改以下内容：

```dotenv
MYSQL_ROOT_PASSWORD=请替换为强密码
BASIC_AUTH_USER=admin
BASIC_AUTH_PASSWORD=请替换为访问密码
AUTH_ALLOWED_EMAILS=your-email@example.com

SMTP_HOST=smtp.example.com
SMTP_PORT=465
SMTP_USER=your-email@example.com
SMTP_PASSWORD=你的邮箱SMTP授权码
SMTP_FROM=your-email@example.com
SMTP_TLS=ssl
```

`AUTH_ALLOWED_EMAILS` 是登录邮箱白名单，支持逗号分隔以及域名通配，例如：

```dotenv
AUTH_ALLOWED_EMAILS=*@qq.com,*@foxmail.com
```

4. 编辑 `deploy/config/.auramate.json`，配置模型 API Key：

```json
{
  "main_model": {
    "provider": "openai",
    "baseURL": "https://api.deepseek.com",
    "model": "deepseek-chat",
    "apiKey": "你的模型API Key"
  },
  "providers": {
    "openai": {
      "apiKey": "你的模型API Key",
      "disabled": false
    }
  }
}
```

实际配置文件还包含其他模型和数据库字段，请只替换对应字段，不要删除完整配置结构。数据库连接会由 `deploy/.env` 中的环境变量覆盖，因此不需要手动把数据库地址改成 `mysql`。

5. 启动服务：

```bash
cd deploy
docker compose up -d --build
docker compose ps
docker compose logs -f api
```

6. 访问 Web 页面：

```text
http://localhost/
```

根据配置输入 Nginx Basic Auth 账号密码，再使用白名单邮箱获取验证码登录。

健康检查：

```bash
curl -u admin:你的BasicAuth密码 http://127.0.0.1/health
```

停止服务：

```bash
docker compose down
```

MySQL 数据保存在 Docker volume 中，普通的 `docker compose down` 不会删除数据库。

### 方式二：本地开发启动

本地开发需要分别启动 MySQL、Go 后端和 Vite 前端。

#### 1. 启动 MySQL

如果本机没有 MySQL，可以使用 Docker 只启动数据库：

```bash
docker run -d \
  --name xiaoying-mysql \
  -p 3306:3306 \
  -e MYSQL_ROOT_PASSWORD=xiaoying \
  -e MYSQL_DATABASE=auramate \
  mysql:8.0 \
  --character-set-server=utf8mb4 \
  --collation-server=utf8mb4_unicode_ci
```

PowerShell 多行命令写法：

```powershell
docker run -d --name xiaoying-mysql -p 3306:3306 `
  -e MYSQL_ROOT_PASSWORD=xiaoying `
  -e MYSQL_DATABASE=auramate `
  mysql:8.0 `
  --character-set-server=utf8mb4 `
  --collation-server=utf8mb4_unicode_ci
```

也可以直接使用本机已有的 MySQL 8.0 实例。

#### 2. 启动 Go 后端

先准备后端配置：

```powershell
Copy-Item .\xiaoying-agent-api\.run\auramate.with-db.json .\xiaoying-agent-api\.auramate.json
```

修改 `xiaoying-agent-api/.auramate.json` 中的数据库密码和模型 API Key：

```json
{
  "database": {
    "host": "127.0.0.1",
    "port": 3306,
    "user": "root",
    "password": "xiaoying",
    "name": "auramate"
  }
}
```

启动后端：

```powershell
cd .\xiaoying-agent-api
go mod download
go run . server --port 3000
```

后端健康检查：

```powershell
Invoke-RestMethod http://localhost:3000/health
```

如果需要在本地测试邮箱验证码登录，还要在启动后端的同一个终端设置：

```powershell
$env:AURAMATE_AUTH_ALLOWED_EMAILS = "your-email@example.com"
$env:AURAMATE_SMTP_HOST = "smtp.example.com"
$env:AURAMATE_SMTP_PORT = "465"
$env:AURAMATE_SMTP_USER = "your-email@example.com"
$env:AURAMATE_SMTP_PASSWORD = "你的邮箱SMTP授权码"
$env:AURAMATE_SMTP_FROM = "your-email@example.com"
$env:AURAMATE_SMTP_TLS = "ssl"
go run . server --port 3000
```

macOS 或 Linux 可使用：

```bash
export AURAMATE_AUTH_ALLOWED_EMAILS="your-email@example.com"
export AURAMATE_SMTP_HOST="smtp.example.com"
export AURAMATE_SMTP_PORT="465"
export AURAMATE_SMTP_USER="your-email@example.com"
export AURAMATE_SMTP_PASSWORD="你的邮箱SMTP授权码"
export AURAMATE_SMTP_FROM="your-email@example.com"
export AURAMATE_SMTP_TLS="ssl"
go run . server --port 3000
```

#### 3. 启动前端

打开新的终端：

```powershell
cd .\xiaoying-agent-web
npm ci
npm run dev:web
```

浏览器访问终端输出的地址，默认通常为：

```text
http://localhost:5173
```

Vite 会把 `/v1` 和 WebSocket 请求代理到 `http://localhost:3000`。

如果需要调试 Electron 桌面端：

```powershell
npm run dev
```

## 模型配置

模型配置位于 `.auramate.json`。常见配置位置：

- 当前目录：`./.auramate.json`
- 用户目录：`~/.auramate.json`
- `$XDG_CONFIG_HOME/auramate/.auramate.json`

也可以通过环境变量指定配置文件：

```powershell
$env:AURAMATE_CONFIG_FILE = "D:\path\to\.auramate.json"
```

常见模型环境变量：

| 环境变量 | 用途 |
| --- | --- |
| `OPENAI_API_KEY` | OpenAI |
| `ANTHROPIC_API_KEY` | Anthropic |
| `GEMINI_API_KEY` | Google Gemini |
| `GROQ_API_KEY` | Groq |
| `GITHUB_TOKEN` | GitHub Copilot |
| `LOCAL_ENDPOINT` | OpenAI 兼容的本地模型服务 |
| `AURAMATE_WORKSPACE` | Agent 工作区目录 |
| `AURAMATE_LOG_PATH` | 日志目录 |

API Key 也可以写入 `.auramate.json` 的 `providers` 或 `main_model` 字段。请勿把包含真实 Key 的配置文件提交到 Git。

## 常用环境变量

| 环境变量 | 说明 | 默认值 |
| --- | --- | --- |
| `AURAMATE_DB_HOST` | MySQL 主机 | `127.0.0.1` |
| `AURAMATE_DB_PORT` | MySQL 端口 | `3306` |
| `AURAMATE_DB_USER` | MySQL 用户 | `root` |
| `AURAMATE_DB_PASSWORD` | MySQL 密码 | 空 |
| `AURAMATE_DB_NAME` | 数据库名称 | `auramate` |
| `AURAMATE_CONFIG_FILE` | 配置文件路径 | 自动查找 |
| `AURAMATE_WORKSPACE` | 工作区路径 | 自动创建 |
| `AURAMATE_AUTH_ALLOWED_EMAILS` | 登录邮箱白名单 | 空 |
| `AURAMATE_SMTP_HOST` | SMTP 服务器 | 空 |
| `AURAMATE_SMTP_PORT` | SMTP 端口 | `465` |
| `AURAMATE_SMTP_TLS` | `ssl` 或 `starttls` | `ssl` |
| `AURAMATE_COOKIE_SECURE` | HTTPS 环境设为 `true` | `false` |

如果邮箱白名单或 SMTP 未配置，验证码登录会返回 `503`，并提示“邮件服务未配置”。

## 构建

### 前端构建

```powershell
cd .\xiaoying-agent-web
npm ci
npm run build
```

构建结果输出到 `xiaoying-agent-web/dist`。

### 后端构建

```powershell
cd .\xiaoying-agent-api
go build -o auramate.exe .
.\auramate.exe server --port 3000
```

macOS 或 Linux：

```bash
cd xiaoying-agent-api
go build -o auramate .
./auramate server --port 3000
```

Windows 生产版打包可执行：

```powershell
cd .\xiaoying-agent-api
.\package_prod.bat
```

### Electron 桌面应用打包

```powershell
cd .\xiaoying-agent-web
npm run package
```

桌面端打包依赖后端 `release_bundle` 中的运行时资源，建议先完成后端生产构建。

## 测试

```powershell
cd .\xiaoying-agent-api
go test ./...

cd ..\xiaoying-agent-web
npm run lint
```

部分集成测试需要本机 MySQL、模型 API Key 或外部服务；缺少依赖时请以单元测试结果为准。

## 数据与持久化

Docker 部署的数据位置：

| 数据 | 位置 |
| --- | --- |
| MySQL | Docker volume `xiaoying_mysql_data` |
| 用户工作区 | `deploy/data/workspace` |
| 后端日志 | `deploy/data/logs` |
| 应用配置 | `deploy/config/.auramate.json` |

以下文件不应提交到 Git：

- `.env`
- `.auramate.json`
- `database/*.db`
- `workspace/`
- `logs/`
- `release-v2/`
- `node_modules/`

## 安全提示

- Agent 具备执行命令和修改文件的能力，请只授予可信用户使用。
- 生产环境建议将 `debug` 设为 `false`。
- 如需每次工具调用前确认，可在 `.auramate.json` 中设置：

```json
{
  "security": {
    "allowAllTools": false
  }
}
```

- 不要直接把后端 `3000` 端口暴露到公网，应通过 Nginx 或网关访问。
- 公网部署建议启用 HTTPS；浏览器麦克风和语音功能通常要求安全上下文。
- 定期备份 MySQL 数据和用户工作区。

## 常见问题

### 前端显示无法连接后端

确认后端已经启动并监听 `3000` 端口：

```bash
curl http://localhost:3000/health
```

如果修改了后端端口，需要同步修改 `xiaoying-agent-web/vite.config.ts` 和 `vite.web.config.ts` 中的代理目标。

### 登录提示“邮件服务未配置”

检查是否设置了 `AURAMATE_AUTH_ALLOWED_EMAILS` 以及完整的 SMTP 配置。SMTP 密码通常需要使用邮箱服务商提供的授权码，而不是邮箱登录密码。

### 后端无法连接 MySQL

确认 MySQL 8.0 已启动、端口可访问，并且 `.auramate.json` 或 `AURAMATE_DB_*` 环境变量中的用户名、密码和数据库名称正确。

### 提示 OpenVINO Embedding 不可用

该能力用于本地向量记忆。缺少 OpenVINO 运行时不会阻止聊天和普通工具调用，但向量检索会自动降级。Docker Linux 镜像默认不包含该运行时。

## 更多文档

- 后端能力与详细配置：`xiaoying-agent-api/README.md`
- Docker 部署、登录与多用户说明：`deploy/README.md`

## 许可证

后端目录包含 MIT License，详情见 `xiaoying-agent-api/LICENSE`。
