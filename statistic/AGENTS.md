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
| `sqlite/` | `sqlite` + linux + (amd64\|386\|arm\|arm64) + CGO | Uses `mattn/go-sqlite3`, requires CGO_ENABLED=1 |

SQLite is linux-only because mattn/go-sqlite3 cross-compilation is painful and the Makefile doesn't bother.

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

## Anti-patterns

- Don't call `AuthUser` in a hot loop — some backends (mysql) hit a map but may lock; cache the `User` at handshake.
- Don't assume `AddUser` is atomic across backends — memory is sync, mysql is async to DB.
