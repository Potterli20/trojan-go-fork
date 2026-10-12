<!-- Generated: 2026-04-20 | Commit: 02d5c12 -->

# api/service/

gRPC service for runtime stats + user management. Tag: `api || full` —— 也就是说
`make`（默认 `-tags full`）**本来就带着它**，只有 `mini` 会排除。

## Files

- `api.proto` — service definition (`TrojanServerService`, `TrojanClientService`)
- `api.pb.go` — **GENERATED**, do not edit
- `api_grpc.pb.go` — **GENERATED**, do not edit
- `server.go` — implements `TrojanServerServiceServer`; talks to `statistic.Authenticator`
- `client.go` — implements `TrojanClientServiceServer`; talks to client-side stats
- `gen.sh` — regenerates pb files. **Manual only, no `go:generate` directive.**

## Regeneration

```
cd api/service
./gen.sh
```

Requires `protoc`, `protoc-gen-go`, `protoc-gen-go-grpc` on PATH. Check script for the exact versions pinned — mismatches cause diff churn.

## Service surface

**TrojanServerService** (server-side)，流的种类以 `api/service/api.proto` 为准：

| RPC | 种类 | 说明 |
|---|---|---|
| `GetTraffic` | unary | 查单个用户的累计流量 |
| `ListUsers` | server-streaming | 每条一个 `UserStatus`；客户端以 `io.EOF` 收尾 |
| `GetUsers` | bidi | 一问一答：Send 一个 `User`，Recv 一个 `UserStatus` |
| `SetUsers` | bidi | `Add/Modify/Delete`；回 `Success`+`Info` |
| `GetRecords` | server-streaming | **实时尾随（tail）**，见下 |
| `SetOutboundConfig` / `GetOutboundConfig` | unary | 运行时改 freedom 的源地址/fwmark |

`GetRecords` 的服务端实现是 `recorder.Subscribe` + `for { select ctx.Done / <-recordChan }`，
**只有客户端取消流才会结束**——所以 `trojan-go -api record` 设计上不会自己退出，
看起来像"挂住"但不是缺陷（实测：对着活着的服务端跑它，服务端日志正常在
`server-side api service is listening`，客户端持续阻塞在等下一条记录）。
别把它"修成"一次快照；要非阻塞查询就用 `-api list`。

**TrojanClientService** (client-side): subset for local stats.

Operations: `Add=0`, `Delete=1`, `Modify=2`.

## CLI 客户端（`api/control`，handler 名 `api`）

- 失败必须变成非零退出码：这个 handler 以前把所有错误 `log.Error` 之后
  `return nil`，而 `main.go` 把 `nil` 读成"接管成功"，于是连不上服务端、未知子命令、
  被服务端拒绝全都 exit 0（实测修复前 `-api list -api-addr ""` → exit 0）。
- 参数缺省时必须 `return option.NotSet()`（见 `option/AGENTS.md`）。
- 查询类（`list`/`get`/`record`）即便业务上"没找到"也照实打印并保持 0；
  只有改状态的 `set` 被服务端拒绝（`Success=false`）才算失败。

## Authentication

The gRPC service itself has no auth. Config 里的键是 **`api.ssl`**
（`enabled` / `cert` / `key` / `verify_client` / `client_cert`；`api.api_tls` 这个键
在本仓库不存在），实现在 `server.go:newAPIServer` 的 `cfg.API.SSL.*` 分支。
**Do NOT expose on public interfaces without a firewall rule** — anyone can add users.

## Conventions

- `User.Hash` is SHA224 hex of password, 56 chars. `User.Password` field is redundant (only used if hash is empty).
- Traffic counters are uint64 bytes, cumulative. Rate fields are bytes/sec.

## Anti-patterns

- Don't edit the `.pb.go` files directly — next regen wipes your change.
- Don't add new RPCs without updating `api.proto` AND running `gen.sh` AND bumping reviewer awareness of the stream semantics.
