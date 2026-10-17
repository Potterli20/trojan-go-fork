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

两层可选凭据，实现在 `auth.go` 与 `server.go:newAPIServer`：

- **`api.token`**：共享密钥。非空时 `newAPIServer` 挂上一元 + 流式两个拦截器，每个 RPC 都要带
  `authorization: Bearer <token>`（裸 token 也接受），`subtle.ConstantTimeCompare` 比较。
  内置命令行用 `-api-token` 传（`api/control/control.go` 的客户端拦截器注入 metadata）。
- **`api.ssl`**（`enabled` / `cert` / `key` / `verify_client` / `client_cert`；`api.api_tls` 这个键
  在本仓库不存在）：`verify_client` 才是鉴权，只开 `enabled` 只是加密。

**默认失败关闭**：`api.enabled` 且监听地址不是回环（含 `api_addr` 留空 = 通配）时，两层都没配
就拒绝启动，`api.allow_insecure: true` 是显式逃生阀。判据 `exposure_gate_test.go`（策略表）、
`token_auth_test.go`（拦截器 + bufconn 端到端）、`wiring_test.go`（闸门确实被建栈同步调用）。

**闸门必须由 `tunnel/trojan.NewServer`/`NewClient` 同步调用**（`api.CheckExposure`，检查器在
`auth.go` 的 `init` 里按 `trojan.Name+"_SERVER"`/`+"_CLIENT"` 注册）。只在 `RunServerAPI` /
`RunClientAPI` 里 return error 是无效的：那两个返回值在调用点被丢进 goroutine，等于什么都没
发生——代理照常跑、API 悄悄不在、日志一行都没有。`Run*API` 里也各查一次，那是纵深防御，
覆盖绕过建栈入口直接调 `api.RunService` 的调用方。

`mini` 构建不含本包，`api.CheckExposure` 查不到检查器就放行，所以闸门不会把 mini 变成硬失败。

## Conventions

- `User.Hash` is SHA224 hex of password, 56 chars. `User.Password` field is redundant (only used if hash is empty).
- Traffic counters are uint64 bytes, cumulative. Rate fields are bytes/sec.

## Anti-patterns

- Don't edit the `.pb.go` files directly — next regen wipes your change.
- Don't add new RPCs without updating `api.proto` AND running `gen.sh` AND bumping reviewer awareness of the stream semantics.
