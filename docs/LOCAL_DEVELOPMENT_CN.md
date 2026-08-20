 

# Sub2API 本地前后端启动说明

本文用于在 Windows PowerShell 中以本机 Go 运行 Sub2API 后端，并以 Vite
运行前端开发服务器。PostgreSQL 和 Redis 需要在本机或可访问的开发环境中
提前启动。

## 1. 前置条件

确认以下命令可用：

```powershell
go version
node --version
pnpm --version
psql --version
redis-cli --version
```

- Go：项目最低要求见 `backend/go.mod`，当前建议使用 Go 1.27.0。
- Node.js：建议使用 Node.js 20 或更新的 LTS 版本。
- pnpm：项目提交了 `pnpm-lock.yaml`。未安装时可运行 `corepack enable`。
- PostgreSQL：本地默认端口为 `5432`。
- Redis：本地默认端口为 `6379`。

## 2. 准备 PostgreSQL 和 Redis

以下示例使用本地 PostgreSQL 超级用户 `postgres` 创建开发账户和数据库。
请将示例密码替换为仅用于本地开发的密码。

```powershell
psql -U postgres -c "CREATE ROLE sub2api LOGIN PASSWORD 'local_dev_password';"
psql -U postgres -c "CREATE DATABASE sub2api OWNER sub2api;"
```

确认依赖可用：

```powershell
pg_isready -h localhost -p 5432 -U sub2api -d sub2api
redis-cli -h localhost -p 6379 ping
```

预期 Redis 返回 `PONG`。若 PostgreSQL 或 Redis 位于其他主机、端口或使用
密码，请在下一步初始化时填写实际连接信息。

## 3. 初始化后端

进入后端目录，下载 Go 依赖后运行一次交互式初始化：

```powershell
cd H:\Develop\Code\ai\app\sub2api\backend
go mod download -x
go run ./cmd/server -setup
```

向导会要求填写：

- PostgreSQL 主机、端口、账号、密码、数据库名和 SSL 模式。
- Redis 主机、端口、密码、数据库编号和 TLS 选项。
- 管理员邮箱和密码。
- HTTP 服务端口，默认是 `8080`。

向导会验证数据库和 Redis 连接、创建所需表结构，并写入 `config.yaml` 和
`.installed`。默认写入当前 `backend` 目录。

若不希望把运行配置写入源码目录，可在初始化和每次启动前指定数据目录：

```powershell
$env:DATA_DIR = 'H:\Develop\Code\ai\app\sub2api\local-data'
go run ./cmd/server -setup
```

此时配置文件位于 `$env:DATA_DIR\config.yaml`。也可以使用 `CONFIG_FILE`
指定一个完整的配置文件路径。

## 4. 启动后端

在第一个 PowerShell 窗口中执行：

```powershell
cd H:\Develop\Code\ai\app\sub2api\backend
$env:DATA_DIR = 'H:\Develop\Code\ai\app\sub2api\local-data' # 未使用时删除此行
go run ./cmd/server
```

服务默认地址为 `http://localhost:8080`。使用 `Ctrl+C` 停止服务。

也可先构建再运行：

```powershell
go build -o bin\server.exe ./cmd/server
.\bin\server.exe
```

## 5. 启动前端

在第二个 PowerShell 窗口中执行：

```powershell
cd H:\Develop\Code\ai\app\sub2api\frontend
corepack enable
pnpm install
pnpm dev
```

打开 `http://localhost:3000`。Vite 会将以下请求代理到
`http://localhost:8080`：

- `/api`
- `/v1`
- `/setup`

后端并非默认端口时，在 `frontend\.env.local` 中创建以下配置后重启
`pnpm dev`：

```env
VITE_DEV_PROXY_TARGET=http://localhost:8081
VITE_DEV_PORT=3000
```

## 6. 常用检查与命令

```powershell
# 后端单元测试
cd H:\Develop\Code\ai\app\sub2api\backend
go test ./...

# 前端类型检查和测试
cd H:\Develop\Code\ai\app\sub2api\frontend
pnpm typecheck
pnpm test:run

# 生成生产前端资源到后端嵌入目录
pnpm build
```

`pnpm build` 会将前端资源输出到 `backend/internal/web/dist`；仅本地开发时
使用 `pnpm dev` 即可，无需每次构建。

## 7. 常见问题

### 数据库或 Redis 连接失败

检查服务是否已启动、主机端口是否正确，以及 `config.yaml` 中的
`database` 和 `redis` 配置。修改后重启后端。

### 前端请求返回 502 或连接失败

确认后端正在监听 `http://localhost:8080`。若后端使用了其他端口，更新
`frontend\.env.local` 的 `VITE_DEV_PROXY_TARGET` 后重启 Vite。

### 需要重新初始化

不要直接删除已有配置或数据。若确实需要重新创建本地开发环境，请先备份
`config.yaml`、`.installed` 和 PostgreSQL 数据库，然后重新执行
`go run ./cmd/server -setup`。
