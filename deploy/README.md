# XiaoYing Docker 部署

这个目录把项目拆成三个容器：

- `web`：Nginx，托管 `xiaoying-agent-web` 的构建产物，并把 `/v1`、`/workspace` 反向代理到后端。
- `api`：Go 后端，监听 3000，连接 MySQL。
- `mysql`：MySQL 8.0，数据保存在 Docker volume `mysql_data`。

## 0. 上传项目

在本地项目根目录打包（不要把 `node_modules`、`dist`、`.git` 和本地 exe 传上去）：

```powershell
tar -czf xiaoying-src.tar.gz `
  --exclude=.git `
  --exclude=.cache `
  --exclude=.backup-* `
  --exclude=.auramate `
  --exclude=.auramate.mcp.json `
  --exclude=deploy/config `
  --exclude=deploy/data `
  --exclude=deploy/.env `
  --exclude=xiaoying-agent-web/node_modules `
  --exclude=xiaoying-agent-web/dist `
  --exclude=xiaoying-agent-web/dist-electron `
  --exclude=xiaoying-agent-web/release-v2 `
  --exclude=xiaoying-agent-api/dist `
  --exclude=xiaoying-agent-api/release_bundle `
  --exclude=xiaoying-agent-api/workspace `
  --exclude=xiaoying-agent-api/database `
  --exclude=xiaoying-agent-api/sensevoice `
  --exclude='*.exe' `
  --exclude='*.log' `
  .
scp .\xiaoying-src.tar.gz root@<SERVER_IP>:/root/
```

然后在服务器上解压：

```bash
mkdir -p /opt/xiaoying
tar -xzf /root/xiaoying-src.tar.gz -C /opt/xiaoying
cd /opt/xiaoying
```

## 1. 准备配置

```bash
cd /opt/xiaoying/deploy
cp .env.example .env
cp /path/to/your/.auramate.json ./config/.auramate.json
mkdir -p data/workspace data/database data/logs
```

编辑 `deploy/.env`，至少修改：

- `MYSQL_ROOT_PASSWORD`
- `BASIC_AUTH_USER`
- `BASIC_AUTH_PASSWORD`
- `AUTH_ALLOWED_EMAILS`
- `SMTP_HOST`、`SMTP_PORT`、`SMTP_USER`、`SMTP_PASSWORD`、`SMTP_FROM`
- `LEGACY_OWNER_EMAIL`（升级前已有会话归属的邮箱）
- `COOKIE_SECURE`（HTTPS 时设为 `true`）

### 本地邮箱验证码登录

网页登录已移除阿里云人机验证，改为本地 SMTP 发码。`AUTH_ALLOWED_EMAILS`
是必须配置的登录邮箱白名单，多个邮箱用逗号分隔；只有名单内的邮箱可以获取
验证码并登录。

也支持域名通配，例如允许所有 QQ 邮箱：

```text
AUTH_ALLOWED_EMAILS=*@qq.com,*@vip.qq.com,*@foxmail.com
```

注意：当前版本不是多租户隔离。不同邮箱登录后共用同一个工作区、模型额度和
会话数据。开放注册前请先确认这是你想要的权限模型；建议至少保留 Nginx
Basic Auth 或 IP 白名单。

常见配置：

- `SMTP_PORT=465`、`SMTP_TLS=ssl`（多数邮箱服务商）
- `SMTP_PORT=587`、`SMTP_TLS=starttls`
- `SMTP_PASSWORD` 填邮箱的 SMTP 授权码/应用专用密码，不是网页登录密码

如果 `AUTH_ALLOWED_EMAILS` 或 SMTP 未配置，发码接口会返回 `503`，页面会提示
“邮件服务未配置”。

### 多用户隔离（第一期 + 第二期）

已经隔离：

- 会话和消息：`sessions.owner_email` + 登录 Cookie 鉴权
- 文件快照：通过会话归属校验
- 工作区：每个邮箱独立目录 `workspace/users/<hash>`
- `/v1/*` 和 `/v1/ws` 必须携带登录会话
- 记忆/知识库：`knowledge_tree.owner_email`
- 定时任务：`scheduled_tasks.owner_email`，后台执行时按任务 owner 建立上下文
- 新闻：`saved_news.owner_email`
- 认知状态：按用户哈希命名空间隔离 key
- token 用量：按当前用户会话汇总
- 提醒通知：只投递给对应登录用户的 WebSocket 连接

升级前已有的会话、知识库、任务和新闻会在启动时归属到
`LEGACY_OWNER_EMAIL`。后台认知/调度任务使用 `SYSTEM_OWNER_EMAIL`。

注意：这是**应用层逻辑隔离**，不是容器级沙箱。Agent 的 `bash` 工具仍然运行在同一个
容器里，理论上可以通过绝对路径读取其他用户的工作区。如果需要防止恶意用户越权，
需要为每个用户使用独立容器/命名空间和独立数据库，这是后续的沙箱化工作。

如果希望每次执行命令/改文件前都在网页里确认，可以把
`deploy/config/.auramate.json` 里的 `security` 改成：

```json
{
  "security": {
    "allowAllTools": false
  }
}
```

生产环境也建议把顶层 `"debug": true` 改成 `"debug": false`。

后端会通过环境变量覆盖 `.auramate.json` 里的数据库地址：

```text
AURAMATE_DB_HOST=mysql
AURAMATE_DB_PORT=3306
AURAMATE_DB_USER=root
AURAMATE_DB_PASSWORD=<deploy/.env 里的 MYSQL_ROOT_PASSWORD>
AURAMATE_DB_NAME=auramate
```

所以不需要手工把 `.auramate.json` 的 `database.host` 改成 `mysql`。

## 2. 启动

```bash
cd /opt/xiaoying/deploy
docker compose up -d --build
docker compose ps
docker compose logs -f api
```

第一次启动时，后端会自动创建 `auramate` 数据库并执行 goose migration。

## 3. 访问

浏览器打开 `http://<服务器公网IP>/`，输入 `deploy/.env` 里的 Basic Auth 账号密码。

健康检查：

```bash
curl -u admin:<BASIC_AUTH_PASSWORD> http://127.0.0.1/health
```

## 4. 常用运维

```bash
cd /opt/xiaoying/deploy

docker compose ps
docker compose logs -f --tail=200 api
docker compose restart api
docker compose down
docker compose up -d --build
```

数据位置：

- MySQL 数据：Docker volume `xiaoying_mysql_data`
- 工作区文件：`deploy/data/workspace`
- 后端日志：`deploy/data/logs`

## 5. 安全提示

- 这个后端默认允许 Agent 在容器内执行命令。不要把 3000 端口直接暴露到公网，只通过 Nginx 访问。
- 默认启用了 Nginx Basic Auth；`/v1` 和 WebSocket 也在保护范围内。
- 建议在阿里云 SWAS 防火墙里只开放 `22` 和 `80`（需要 HTTPS 时再开 `443`）。
- 浏览器麦克风/语音功能通常要求 HTTPS，纯 IP 的 HTTP 访问可能无法使用录音。

## 6. 关于向量记忆

当前 Linux 镜像没有包含 OpenVINO `ov-embed` 运行时。后端启动时会记录一条
`Local OpenVINO embedder unavailable` 警告并继续运行，聊天和工具功能正常，
向量记忆检索会降级关闭。

如果要启用向量记忆，需要准备 Linux 版 `ov-embed` 和
`all-MiniLM-L6-v2` 的 OpenVINO 模型，放到容器的 `/app/openvino_embed`
目录，并设置：

```text
AURAMATE_OPENVINO_EMBED_BINARY_PATH=/app/openvino_embed/ov-embed
AURAMATE_OPENVINO_EMBED_MODEL_DIR=/app/openvino_embed/model
```

运行时镜像使用阿里云 Debian 源，前端镜像使用阿里云 Alpine 源，避免国内
服务器从境外源安装依赖过慢。
