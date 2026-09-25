# 巡台

运维闭环控制台。平台保存配置、工单和剧本，不重做 Prometheus，也不代替集群里的 Agent。看图仍交给 Grafana。

八个模块按依赖顺序是：底座、服务树、工单、任务、监控、集群、发布、数据库。

## 本地运行

需要 Go 1.24 和 Node.js。

1. 复制环境变量示例，并填上自己的值。不要把填好的 `.env` 提交进仓库。

```powershell
Copy-Item .env.example .env
```

必填：

- `XUNTAI_JWT_SECRET`：签发登录令牌。不设的话进程会直接退出。
- `XUNTAI_ADMIN_PASSWORD`：初始用户周宁、林夏、许衡、陈舟共用这一个密码。

可选：

- `XUNTAI_MYSQL_DSN`：不设时使用本地 SQLite，文件在 `data/xuntai.db`。
- `XUNTAI_HTTP_ADDR`：默认 `:8080`。
- `XUNTAI_SCOPE_FILTER`：默认按服务树收窄列表。设为 `0` 才看全量。
- `XUNTAI_TASK_MOCK`、`XUNTAI_ROLLOUTS_MOCK`：只给本地演示。生产不要打开。

2. 启动接口：

```powershell
go run ./cmd/server
```

3. 启动页面：

```powershell
cd demo
npm install
npm run dev
```

页面在 http://127.0.0.1:5173 ，接口在 http://127.0.0.1:8080 。前端把 `/api` 转到本机 8080。

## 测试

```powershell
go test ./...
cd demo
npm test
npm run build
```

## 文档

设计说明在 `docs/`：

- `docs/p0-剧本与对象.md`
- `docs/p1-权限模型.md`
- `docs/p2-监控对象依赖.md`
- `docs/p2-cicd对象化与生产门禁.md`
- `docs/p2-灰度执行器.md`
- `docs/p2-故障自愈.md`
- `docs/p3-数据库模块接入.md`

## 边界

- 生产镜像不能从实例接口或发布确认直接改，要走已发布的生产发布或回滚剧本。
- 告警自愈只创建预授权剧本的 Run。没有绑定剧本的规则只告警、只找人。
- `cmd/agent` 和 `cmd/webhook` 仍是入口占位，没有接真实 Agent 和 Alertmanager。
- 数据库模块登记实例和变更语句，不连接 MySQL 去执行 SQL。
