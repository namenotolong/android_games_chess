# 棋类联网服务

Go、Hertz、SQLite 双人联网对局服务，支持五子棋和中国象棋，提供房间创建/加入、基于 WebSocket 的实时棋盘同步，以及服务端落子规则校验。

## 环境要求

- Go 1.26+
- SQLite 驱动 github.com/mattn/go-sqlite3 使用 CGO；macOS/Linux 开发环境需要 C 编译器

## 启动

从项目根目录执行：

    go mod tidy
    go run ./cmd/server

默认监听 :8888，数据库创建在 data/gomoku.db。可通过 HTTP_ADDR 和 DATABASE_PATH 环境变量配置服务地址与数据库路径。更新代码后需重启正在运行的服务，使新增接口生效。

Android 模拟器通过 `http://10.0.2.2:8888` 访问宿主机；实体设备需将 Android 客户端 `OnlineGameClient.kt` 中的地址改为开发电脑的局域网 IP。

## 接口

- `GET /healthz`：服务健康检查
- `POST /api/v1/rooms`：创建房间，JSON `{ "displayName": "玩家" }`；返回唯一的 4 位数字房间号，号码池耗尽时返回 503 和“线上人数过多，创建失败，请稍后再试”
- `POST /api/v1/rooms/{roomCode}/join`：加入房间，JSON `{ "displayName": "玩家" }`
- `POST /api/v1/xiangqi/rooms`：创建象棋房间
- `POST /api/v1/xiangqi/rooms/{roomCode}/join`：加入象棋房间
- `GET /api/v1/matches/{roomId}`：携带 `Authorization: Bearer <playerToken>` 查询对局
- `GET /api/v1/ws?roomId={roomId}&token={playerToken}`：WebSocket 实时对局。支持 `type=move`、`restart`、`request_undo`、`request_swap_colors`、`accept_action`、`reject_action` 和 `resign`；所有修改都携带 `expectedRevision`，服务端更新 `rooms` 中的当前局快照并广播

悔棋和交换先手需要对方同意，等待回应时棋盘不可落子。悔棋按请求发起人定位其最近一步，并撤销该步及其后对手最多一步；任一玩家均可请求。交换先手获同意后会交换黑白棋并清空棋盘，以黑棋重新先行。重开由任一玩家触发后立即同步给双方；认输会立即结束本局并判对手获胜。当前只在房间快照中保留最近两步及对应撤销快照，不保留完整对局历史。迁移会删除旧 `moves` 表。

SQLite 迁移会在启动时自动执行，并为已有房间增加棋色及最近一步快照字段。

象棋沿用 `rooms` 房间表，通过 `game_type=XIANGQI` 区分棋种，棋盘快照为 10×9。象棋 WebSocket 复用 `/api/v1/ws`，`move` 消息携带 `fromRow`、`fromColumn`、`toRow`、`toColumn` 和 `expectedRevision`；走法由服务端验证。象棋支持重开与认输。启动时自动应用迁移，为房间表添加棋种及棋盘列数。
