<!-- Generated: 2026-04-20 | Commit: 02d5c12 -->

# api/service/

gRPC service for runtime stats + user management. Tag-gated: `-tags api`.

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

**TrojanServerService** (server-side):
- `GetTraffic(User) → Traffic{Upload, Download}`
- `GetUsers(streaming) → UserStatus{Traffic, SpeedCurrent, IPCurrent}`
- `SetUsers(streaming of UserStatus with operations) → SetUsersResponse`
- `ListUsers(stream) → UserStatus`

**TrojanClientService** (client-side): subset for local stats.

Operations: `Add=0`, `Delete=1`, `Modify=2`.

## Authentication

The gRPC service itself has no auth. Config `api.api_tls` can enable TLS for transport. **Do NOT expose on public interfaces without a firewall rule** — anyone can add users.

## Conventions

- `User.Hash` is SHA224 hex of password, 56 chars. `User.Password` field is redundant (only used if hash is empty).
- Traffic counters are uint64 bytes, cumulative. Rate fields are bytes/sec.

## Anti-patterns

- Don't edit the `.pb.go` files directly — next regen wipes your change.
- Don't add new RPCs without updating `api.proto` AND running `gen.sh` AND bumping reviewer awareness of the stream semantics.
