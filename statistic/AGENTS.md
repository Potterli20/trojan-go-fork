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
| `mysql/` | `mysql` | Polls DB every N seconds; syncs users; writes traffic back |
| `sqlite/` | no build tag; platform-gated: linux + (amd64\|386\|arm\|arm64) | Pure-Go driver (`Potterli20/sqlite` + `modernc.org/sqlite`), no CGO needed. Enabled by the `sqlite` field of the memory config, not by a tag |

SQLite is linux-only because the platform build tags in `statistic/sqlite` restrict it; on every other platform
`NewSqlitePersistencer` returns an error at startup rather than a silent no-op.

## Authentication flow

1. `tunnel/trojan/server.go` reads 56-hex-char header.
2. Calls `auth.AuthUser(hash)`.
3. On success, holds returned `User` for the conn lifetime; calls `AddTraffic` per Read/Write.
4. Speed/IP limits enforced via `common/ratelimiter`.

## Polling (mysql)

`mysql/mysql.go` polls every `check_rate` seconds. Traffic uploaded back in same interval. Batched — not per-conn. Lost traffic on crash.

## Conventions

- Hash format = SHA224 hex (56 chars), lowercase. Mismatched case = auth fail.
- `IPLimit=0` means unlimited. `SpeedLimit=0` means unlimited.
- Traffic counters are monotonic bytes. Rate is a windowed derivative.

## 启动期校验

- **`password` 列表里出现空串会让服务端拒绝启动**（`memory.NewAuthenticator` 在任何
  资源分配之前返回错误）。原因：配置文件这条路径原来是 `hash := SHA224String("")` +
  `AddUser(hash)`，而空串的 SHA224 是公开常量 ⇒ 等于开了一条谁都能用的免密通道；
  真机实测修复前它不但能起来，还会在后面的重定向地址检查上以一句无关的
  `invalid redirect address ... dial tcp :0` 失败，根因完全看不见。
  `easy` 模式（`-password`）本来就 `log.Fatal` 拒绝空密码，两条路径现在一致。
- `acquireAuth` 失败时**返回**原始错误（不再 log 完就 `return nil`），
  否则 `trojan.NewServer` 只能造一句通用文案，根因在错误链里丢失。

## Anti-patterns

- Don't call `AuthUser` in a hot loop — some backends (mysql) hit a map but may lock; cache the `User` at handshake.
- Don't assume `AddUser` is atomic across backends — memory is sync, mysql is async to DB.
