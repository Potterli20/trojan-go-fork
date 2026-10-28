<!-- Generated: 2026-04-20 | Commit: 02d5c12 -->

# statistic/

User authentication + traffic accounting. Three pluggable backends.

## Interface (`statistics.go`)

```go
type User interface {
    Hash() string
    SetTraffic(upload, download uint64)
    GetTraffic() (uint64, uint64)
    ResetTraffic()
    AddTraffic(upload, download int)
    SetIPLimit(int); GetIPLimit() int
    AddIP(ip string) bool  // false if over limit
    DelIP(ip string) bool
    GetIP() int
    SetSpeedLimit(upload, download int)
    GetSpeedLimit() (upload, download int)
    GetSpeed() (upload, download uint64)  // rate
}

type Authenticator interface {
    AuthUser(hash string) (valid bool, user User)
    AddUser(hash string) error
    DelUser(hash string) error
    ListUsers() []User
    Close() error
}
```

Registered via `RegisterAuthenticatorCreator(name, fn)`.

## Backends

| Backend | Build tag | Notes |
|---|---|---|
| `memory/` | always (base) | Static password list from config; no runtime changes except via gRPC |
| `mysql/` | `mysql` | Polls DB every N seconds; syncs users; writes traffic back. Idempotently creates the `users` table and adds missing `maxip` / `expire` columns at startup (`ensureUsersTable`). Connection params go through `mysql.NewConfig` + `NewConnector` (escaping + dial/read/write timeouts); TLS rides on the connector, not `RegisterTLSConfig`. Tests: `sync_users_test.go`, `dsn_config_test.go` via the `mysqlDB` seam |
| `sqlite/` | no build tag; platform-gated: linux + (amd64\|386\|arm\|arm64) | Pure-Go driver (`Potterli20/sqlite` + `modernc.org/sqlite`), no CGO needed. Enabled by the `sqlite` field of the memory config, not by a tag |

SQLite is linux-only because the platform build tags in `statistic/sqlite` restrict it; on every other platform
`NewSqlitePersistencer` returns an error at startup rather than a silent no-op.

## Authentication flow

1. `tunnel/trojan/server.go` reads 56-hex-char header.
2. Calls `auth.AuthUser(hash)`.
3. On success, holds returned `User` for the conn lifetime; calls `AddTraffic` per Read/Write.
4. Speed/IP limits enforced via `common/ratelimiter`.

## 限速排队的唤醒源（`statistic.PacedTrafficMeter`）

`AddSentTraffic`/`AddRecvTraffic` 只有**用户级** ctx 可听，而按字节排队可能持续几十秒到几小时
（`speed_limit: 1` 的 B/s 配 8KB 中继块 ≈ 2 小时）。relay 拆会话走的是关连接，`Proxy.Close` 的 5s
预算只是放弃等待、叫不醒它——那条协程连同从 `boundedBufPool` 借走的中继 buffer 会一直留在池外。

所以 `User` 额外实现可选接口 `PacedTrafficMeter`（`AddSentTrafficContext` / `AddRecvTrafficContext`，
**不改** `TrafficMeter`，既有实现与替身不受影响）：会话侧传自己的 ctx，`memory.pacingContext` 用
`context.WithCancel(u.ctx)` + `context.AfterFunc(session, cancel)` 把两个唤醒源合成**只活在这次等待里**
的子 ctx，`defer release()` —— 子 ctx 挂在 `u.ctx` 的 children 上，不释放就是每块一次累积。
`session == nil` 时逐字退回老行为。

两条口径别踩：① 会话 ctx 只在 **Auth 成功之后**建立（失败路径不保证每个出口都走 Close）；
② **等待被打断不等于流量没发生**，`u.Sent`/`u.Recv` 照记 —— 判据
`statistic/memory/limiter_stop_test.go`（synctest 假时钟量取消后的返回耗时）与
`tunnel/trojan/paced_session_ctx_test.go`（接线 + Close 幂等）都钉了这一点。

## Polling (mysql)

`mysql/mysql.go` polls every `check_rate` seconds. Traffic uploaded back in same interval. Batched — not per-conn. Lost traffic on crash.

Table is `username,password,quota,download,upload,maxip,expire` (authoritative copy:
`example/mysql.sql`). `expire` is a Unix-second deadline (`0` = never) and `quota` is a byte
allowance (`<0` unlimited, `0` = no allowance), so the DDL default for `quota` is **-1**.
Each round pushes `expire` and `quota`+used-base into the in-memory user, so auth is refused
**at once** rather than at the next round (the old behaviour left a `check_rate` window).
The three "this user must not stay" exits (expired / over quota / row deleted) **flush before
deleting** — `DelUser` runs `User.Close()`, whose `ResetTraffic` zeroes without writing.
On a failed `UPDATE` the already-reset increment is added back via `RestoreTraffic`
(deliberately bypasses the rate limiters). Do **not** use `RowsAffected` to infer
"user was deleted from the DB" — MySQL reports *changed* rows by default (`ClientFoundRows` off),
so a zero-increment user reports 0 and would be wrongly dropped every cycle.

## Conventions

- Hash format = SHA224 hex (56 chars), lowercase. Mismatched case = auth fail.
- `IPLimit=0` means unlimited. `SpeedLimit=0` means unlimited.
- Traffic counters are monotonic bytes. Rate is a windowed derivative.
- Expiry (`expire`, Unix seconds, `0` = never) comes from the persistence backends and is surfaced
  through `statistic.Metadata.GetExpire`. Both backends now refuse expired users **in `AuthUser`**:
  `memory.AuthUser` checks `isExpired || isOverQuota`, and mysql/sqlite push their column values
  into the in-memory user (`SetUserExpire` / `SetUserQuota`, which deliberately do not write back
  to the persistencer). sqlite additionally keeps the row (expiry ≠ deletion — renewals need it).
  In-memory users (config `password` array) are always `0`/unchecked, i.e. permanent.
- **`memory/refresh.go`: 配了持久化后端时起一条 `limitsRefresher`**（默认 60s，间隔存
  `sqliteRefreshInterval` 原子值，判据在运行期调小它），用**一次** `ListUser` 全表读把
  `expire` / `maxip` / 上下行限速重新推进已有内存用户——面板直接 `UPDATE` 库里这几列从此
  **免重启**生效（原先只在 `NewAuthenticator` 装载时读一次，属于「接受但不生效」）。三条边界：
  ① 不新增也不删除用户（库里多出的行不该把 `DelUser` 掉的人捞活）；② **绝不碰 `quota` /
  `quotaChecked`** —— SQLite 表没有额度列，打开判定等于给所有 SQLite 用户设了 `quota=0`；
  ③ 走 `User` 上的内部 setter 而不是 `SetUserIPLimit` / `SetUserSpeedLimit`，后两个是运维改值
  的入口、会 `SaveUser` 整行写回，读侧调它们就是每轮重抄一遍并与 `batchTrafficUpdater` 抢流量列。
  **MySQL 侧必须关掉这条轮**（`WithoutPersistencerRefresh(ctx)`）：`mysql.Authenticator` 内嵌同一个
  memory 认证器，`mysql` 与 `memory.sqlite` 可以同时生效，两边推同一批用户就是互相盖值——
  SQLite 那条 `expire=0` 的镜像行会把 MySQL 里已到期的人复活成永不过期。
  判据 `sqlite_refresh_test.go`（到期/续期两个方向 + 抑制闸门 + 「不碰 quota」）。

## 启动期与运行时校验

- **空密码在三个入口都被堵死**（`SHA224("")` 是公开常量，注册成用户 = 一条免密通道）：
  1. 配置文件：`memory.NewAuthenticator` 在**任何资源分配之前**拒绝
     （再往下已打开 sqlite 持久化器并起后台协程）；`easy` 模式（`-password`）
     本来就 `log.Fatal` 拒绝，两条路径现在一致。
  2. 运行时 gRPC：`memory.AddUser(hash)` 拒绝 `""` 与 `SHA224("")`。
     这条是必须的 —— `api/service` 的 SetUsers 会做
     `if Hash == "" { Hash = SHA224(Password) }` 再 `AddUser`，所以
     `-api set -add-profile -target-password ""` 原本能在跑着的进程上现场开出免密用户，
     而 **gRPC API 自身没有任何鉴权**。真机实测（修复前的二进制）：该调用 exit=0
     且空密码哈希出现在 `-api list` 里；修复后 exit=1、命中 0 次。
  3. 持久化后端装载：`ListUser` 回调跳过空密码行并打 ERROR（老版本在 AddUser 与
     SetKeyShare 之间崩溃会留下这种行），跳过而不是拒启，是为了不把已有部署直接锁死（bricked）。
- `acquireAuth` 失败时**返回**原始错误（不再 log 完就 `return nil`），
  否则 `trojan.NewServer` 只能造一句通用文案，根因在错误链里丢失。

## Anti-patterns

- Don't call `AuthUser` in a hot loop — some backends (mysql) hit a map but may lock; cache the `User` at handshake.
- Don't assume `AddUser` is atomic across backends — memory is sync, mysql is async to DB.
