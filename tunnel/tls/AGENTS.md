<!-- Generated: 2026-04-20 | Commit: 02d5c12 -->

# tunnel/tls/

TLS transport + uTLS client fingerprinting + ACME/Let's Encrypt integration.

## Files

- `tunnel.go`, `config.go` — registration + `TLSConfig` (cert, key, SNI, ALPN, Fingerprint, Verify, ReuseSession, SessionTicket, KeyLog, ACME…)
- `client.go` — outbound TLS dial; routes through uTLS if `Fingerprint != ""`
- `server.go` — inbound TLS listener; ACME autocert, OCSP stapling, cert reloader
- `fingerprint/` — uTLS `ClientHelloID` allow-list (see `fingerprint/fingerprint.go`)
- `certreloader.go` — hot-reloads cert/key on file mtime change
- `keylogger.go` — SSLKEYLOGFILE for Wireshark debugging

## Fingerprint selection

Config `fingerprint` string → `fingerprint.ParseClientHelloID(name)` → `utls.ClientHelloID`. Supported IDs are vendored from `refraction-networking/utls`. **Updating uTLS requires updating `fingerprint/fingerprint.go` allow-list.** Unknown names return error.

If `fingerprint` empty: use stdlib `crypto/tls` (no anti-fingerprinting).

## ACME

`config.ACME.Enabled=true` → uses `mholt/certmagic` internally (via go.mod). Domains + email required. Cert stored in `certmagic`'s default path. Not compatible with `certpath`/`keypath` — one or the other.

## Session resumption

- `ReuseSession` → session cache on client
- `SessionTicket` → server issues tickets (disabled by default; enabling reduces TLS fingerprint uniqueness)

## Conventions

- **keyLogger 的所有权跟着 success 标志走，两侧同一形态。** `os.OpenFile(key_log)` 在
  `NewClient`/`NewServer` 里都发生在"对象已经能接手这个句柄"之前，而唯一的归还入口是各自的
  `Close()`；此后任何错误返回都不交出对象，句柄就永久漏掉（实测 20 次失败留 20 个 fd，
  判据 `keylogger_fd_test.go` / `keylogger_fd_server_test.go`）。服务端 2026-10-28 才补上
  客户端那条 `keyLoggerOwned` defer —— 服务端在此之后新增的错误分支（`ValidateCurvePreferences`）
  就是触发点。**加新错误分支时先看这条 defer 还罩不罩得住。**
- Cert reloader polls mtime every `certreloader.go` interval. No SIGHUP. No inotify.
- Server TLS config is built ONCE per instance; mutations (e.g. new cert) swap via `GetCertificate` callback.

## Anti-patterns

- Don't set `InsecureSkipVerify=true` in client without explicit user opt-in — config gate is `Verify=false`.
- Don't ship a new uTLS fingerprint without adding to the allow-list; silent fallback hides breakage.
