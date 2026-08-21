<!-- Generated: 2026-04-20 | Commit: 02d5c12 -->

# tunnel/trojan/

Trojan protocol implementation. The namesake.

## Files

- `tunnel.go` — `Tunnel{}.Name() = "TROJAN"`, registers in `init()`
- `client.go` — outbound: SHA224(password) + CRLF + metadata + CRLF + payload
- `server.go` — inbound: reads 56-byte hex hash, validates against user DB, else redirects
- `conn.go` — wraps inner conn, lazy-writes header on first `Write`
- `packet.go` — UDP over TCP; length-prefixed datagrams per trojan spec
- `config.go` — `TrojanConfig`: `LocalAddress`, `RemoteAddress`, `Password[]`, `API`, `DisableHTTPCheck`

## Wire format (outbound handshake)

```
56 hex digits (SHA224(password))  CRLF
1 byte CMD (0x01=TCP, 0x03=UDP)
tunnel.Metadata.Address (variable)
CRLF
payload…
```

Hash computed in `common.SHA224String(password)`.

## Server dispatch

On auth success: conn is handed to outer stack as `tunnel.Conn` with `Metadata()` parsed from header.
On auth failure:
1. If `DisableHTTPCheck=false`, attempt HTTP sniffing; if it looks like HTTP and a redirect target exists, proxy to it (`redirector/`).
2. Else feed raw bytes to misdirection target via `recorder/`.

Never close immediately — that's fingerprintable.

## UDP (`packet.go`)

Each datagram: `Address | len(2B BE) | CRLF | payload`. `PacketConn.ReadFrom/WriteTo` serialize/deserialize over the same TCP conn. Trojan has no native UDP — it's tunneled inside the TLS stream.

## Auth

User DB lookup via `statistic.Authenticator` (`statistic/` subtree). `statistic/memory` for password list; `mysql`/`sqlite` for dynamic users. `api/service/` gRPC can add/remove users at runtime.

## Conventions

- Never write header eagerly — wait for first payload `Write`. Empty-connection fingerprinting countermeasure.
- Password list hashes computed **once** at server startup (`server.go` builds a map[hash]user).

## Anti-patterns

- Don't log the password or hash at Info level. Debug only.
- Don't respond with a distinct error on bad auth — must be indistinguishable from valid-but-idle TLS.
