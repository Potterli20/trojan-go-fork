<!-- Generated: 2026-04-20 | Commit: 02d5c12 -->

# tunnel/shadowsocks/

AEAD obfuscation layer wrapping trojan payload. Defeats pattern-match censors targeting TLS-then-plaintext.

## Files

- `tunnel.go`, `config.go` — `ShadowsocksConfig{Enabled, Method, Password}`
- `client.go`, `server.go` — wrap inner `Conn` with `shadowaead.Cipher`

## Dependency

Uses `shadowsocks/go-shadowsocks2/shadowaead`. The library has a stream capacity check that interferes with trojan framing:

**Required env var in tests and prod**:
```
SHADOWSOCKS_SF_CAPACITY=-1
```
Disables the check. See `Makefile::test` and CI workflows. Without it, `go test -v ./...` hangs on shadowsocks suites.

## Supported methods

`AEAD_CHACHA20_POLY1305`, `AEAD_AES_256_GCM`, `AEAD_AES_128_GCM`. Stream ciphers (rc4, table, aes-cfb) intentionally NOT supported — insecure.

## Stack position

Client side:
```
[transport tls shadowsocks trojan]
```
Shadowsocks wraps the trojan handshake — so the TLS-decrypted bytes look like random shadowsocks, not like a distinguishable trojan header. Both ends must agree on method/password.

## Conventions

- `Method` strings are v2ray-style uppercase; `go-shadowsocks2` wants lowercase — conversion happens in `config.go`.
- Password is key-derived via HKDF; not the same password field as trojan user auth.

## Anti-patterns

- Don't enable without trojan underneath — shadowsocks alone provides no proxy semantics here.
- Don't confuse `shadowsocks.Password` with `trojan.Password[]` — distinct config fields, distinct purposes.
