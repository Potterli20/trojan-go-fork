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

- Cert reloader polls mtime every `certreloader.go` interval. No SIGHUP. No inotify.
- Server TLS config is built ONCE per instance; mutations (e.g. new cert) swap via `GetCertificate` callback.

## Anti-patterns

- Don't set `InsecureSkipVerify=true` in client without explicit user opt-in — config gate is `Verify=false`.
- Don't ship a new uTLS fingerprint without adding to the allow-list; silent fallback hides breakage.
