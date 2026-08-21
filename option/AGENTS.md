<!-- Generated: 2026-04-20 | Commit: 02d5c12 -->

# option/

Startup-mode selector. Not CLI flag parsing — that's `flag` stdlib in `main.go`.

## API

```go
type Handler interface {
    Name() string
    Handle() error
    Priority() int
}
RegisterHandler(h Handler)
PopOptionHandler() Handler  // highest priority first
```

## How main.go uses it

```go
for {
    h := option.PopOptionHandler()
    if h == nil { log.Fatal("no handler") }
    err := h.Handle()
    if err == nil { return }       // handler consumed the run
    if err.Error() == "not set" { continue }  // try next
    log.Fatal(err)
}
```

Each handler checks its flag (e.g. `-version`, `-test`). If flag not set → returns `common.NewError("not set")` → loop tries next handler. Highest `Priority()` wins.

## Registered handlers (see `component/other.go`)

- `version` — print version, exit
- `test` — validate config + exit
- `info` — print config-format info
- `default` — actually run the proxy (lowest priority; the catch-all)

## Conventions

- **Return exactly `common.NewError("not set")`** when your flag isn't specified — the string is compared literally in `main.go`.
- Priority is an `int`; higher runs first. `default` must be the lowest.
- Handlers use package-level `flag.*Var` calls in their `init()` — that's how the flags appear in `-h`.

## Anti-patterns

- Don't add a handler that returns `nil` unconditionally — it becomes a black hole, no other handler ever runs.
- Don't parse `os.Args` manually; always go through the `flag` package so `-h` stays consistent.
