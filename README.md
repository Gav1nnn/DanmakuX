# DanmakuX

DanmakuX 是一个轻量级 Go 实时弹幕项目骨架，也可以作为小型开源框架继续扩展。

## 功能特性

- WebSocket 长连接实时弹幕（支持多房间）
- 类 B 站风格前端 Demo（黑色视频占位区 + 滚动弹幕渲染）
- Redis Pub/Sub 跨节点广播
- MySQL 弹幕持久化（异步批量写入）
- Redis 限流（用户/IP/房间三层）
- JWT 鉴权（提供游客 token 接口）

## 架构流程

```text
客户端
  -> POST /api/v1/auth/guest 获取 JWT
  -> GET /ws?room_id=xxx&token=xxx 建立 WebSocket
      -> MessageService
          -> 限流（Redis）
          -> 广播发布（Redis Pub/Sub）
          -> 异步入库（MySQL）

Redis Pub/Sub
  -> 各节点订阅房间频道
  -> 节点内 Hub 向本机房间连接广播
```

## 本地启动（推荐流程）

1. 启动依赖服务（MySQL + Redis）

```bash
make up
```

2. 复制环境变量文件（默认使用应用账号 `danmakux/danmakux` 连接 MySQL）

```bash
cp .env.example .env
```

3. 安装依赖并运行

```bash
make deps
make run
```

说明：
- 项目已内置 `.env` 自动加载（`godotenv/autoload`），通常不需要手动 `source .env`。
- `make run` 也会自动读取 `.env`。
- 默认将 Docker MySQL 映射到宿主机 `3307` 端口，避免和本机已有 MySQL `3306` 冲突。

4. 健康检查

```bash
curl http://127.0.0.1:8080/healthz
```

5. 打开调试页面

推荐直接访问：

```text
http://127.0.0.1:8080/examples/ws_client.html
```

输入 `user_id`、`room_id` 后连接并发送弹幕。

## 接口说明

- `GET /healthz`
- `POST /api/v1/auth/guest`
  - 请求体：`{"user_id":"u1"}`
- `GET /api/v1/rooms/:room_id/messages?limit=50&before=2026-03-31T00:00:00Z`
- `GET /ws?room_id=room-1&token=<jwt>`

WebSocket 上行消息：

```json
{"type":"danmaku","content":"hello world"}
```

WebSocket 下行消息：

```json
{
  "type":"danmaku",
  "message_id":"uuid",
  "room_id":"room-1",
  "user_id":"u1",
  "content":"hello world",
  "node_id":"node-1",
  "timestamp":"2026-03-31T12:00:00Z"
}
```

## 多节点演示

两实例连接同一个 Redis/MySQL 即可：

- 节点 A：`HTTP_ADDR=:8080 NODE_ID=node-a`
- 节点 B：`HTTP_ADDR=:8081 NODE_ID=node-b`

这样 A 节点收到的弹幕会通过 Redis Pub/Sub 广播到 B 节点。

## 常见问题排查

1. 报错：`panic: MYSQL_DSN is required`
- 原因：环境变量未加载，程序拿不到 `MYSQL_DSN`。
- 解决：
  1. `cp .env.example .env`
  2. 直接执行 `make run`（无需手动 source）

2. 报错：`.env: parse error near '&'`
- 原因：你用 shell 执行 `source .env` 时，`MYSQL_DSN` 里的 `&` 被当成 shell 特殊符号。
- 解决：
  - 已在 `.env.example` 中给 `MYSQL_DSN` 加了引号；
  - 或者不要手动 `source`，直接 `make run`。

3. 提示：`docker-compose.yml: the attribute version is obsolete`
- 已移除 `version` 字段，可忽略该问题。

4. 报错：`Access denied for user ...`（MySQL 鉴权失败）
- 常见于你本地已有旧的 MySQL 数据卷，初始化账号和当前配置不一致。
- 解决：重置数据库卷并重新初始化

```bash
make reset-db
cp .env.example .env
make run
```

5. 报错仍然是 `...@localhost` 或连接到了错误 MySQL
- 原因：你本机可能已有 MySQL 在 `127.0.0.1:3306`，程序连错实例。
- 当前默认配置使用 `127.0.0.1:3307`（Docker MySQL）。
- 请确认 `.env` 中 `MYSQL_DSN` 端口为 `3307`。

## 开发命令

```bash
make deps     # 拉依赖
make up       # 启动 MySQL/Redis
make reset-db # 重置数据库卷并重启依赖（会清空本地 MySQL 数据）
make run      # 启动服务（自动加载 .env）
make test     # go test ./...
make build    # 构建二进制到 bin/danmakux
make load-test                      # 执行默认压测
make load-test ARGS="-clients 80 -duration 30s -room-count 4"
make k6-load                        # 使用 k6 执行 WebSocket 压测
make k6-load ARGS="-e VUS=40 -e DURATION=30s -e ROOM_COUNT=5"
make down     # 停止依赖服务
```

## 压测脚本

项目内置 Go 原生压测脚本：`scripts/load/ws_load.go`（无需安装 k6）。
同时也提供了 `k6` 版本：`scripts/k6/danmaku.js`。

默认行为：
- 并发建立 WebSocket 连接
- 每个连接按固定间隔发送弹幕
- 统计发送/接收吞吐、限流次数、端到端回显延迟（p50/p95/p99）

示例：

```bash
make run # 先在另一个终端启动后端
make load-test
make load-test ARGS="-clients 100 -duration 45s -send-interval 900ms -room-count 5"
k6 run -e VUS=40 -e DURATION=30s -e ROOM_COUNT=5 ./scripts/k6/danmaku.js
```

注意：
- 默认限流较严格（尤其 `LIMIT_IP_COUNT=20` / `3s`），当并发较高时出现大量 `rate-limited responses` 属于预期。
- 如果你要测系统上限，可临时调大 `.env` 的限流参数后重启服务再压测。

## 目录结构

```text
cmd/danmakux/main.go       # 入口与依赖装配
internal/api               # HTTP 路由
internal/ws                # WebSocket 连接处理
internal/room              # 节点内房间管理
internal/broker            # Redis Pub/Sub 抽象与实现
internal/service           # 消息主流程（校验/限流/发布/落库）
internal/limiter           # Redis 限流器
internal/repository        # MySQL 仓储层
internal/store             # Redis/MySQL 初始化
pkg/protocol               # 协议模型
```

## Roadmap

- 增加敏感词与审核钩子
- 增加 `/metrics` + Prometheus 监控
- 增加 k6 压测脚本
- 可选接入 Kafka 提升消息持久性

## License

MIT
