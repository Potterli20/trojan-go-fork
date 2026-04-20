<!-- Generated: 2026-04-20 | Commit: 02d5c12 -->

# config/

Context-based config registry. Modules self-describe their config struct; JSON/YAML populates it into `context.Context`.

## API

```go
RegisterConfigCreator(name string, creator func() interface{})
WithJSONConfig(ctx, data []byte) (context.Context, error)
WithYAMLConfig(ctx, data []byte) (context.Context, error)
FromContext(ctx, name string) interface{}
```

## Registration

Each tunnel/proxy calls `RegisterConfigCreator("NAME", func() interface{} { return &XxxConfig{Defaults…} })` in `init()`. The creator returns a zero/default struct the config loader fills.

## Loading flow

1. Raw bytes come from `main.go` via `-config file.json|yaml`.
2. `WithJSONConfig` / `WithYAMLConfig` iterates **all registered creators**, decodes the same document into each struct, and stores under `ctx.Value(name+"_CONFIG")`.
3. Modules retrieve: `cfg := config.FromContext(ctx, Name).(*XxxConfig)`.

## Conventions

- **One flat JSON/YAML document**, all modules consume the same top-level. Fields unused by a module are ignored.
- Defaults go in the creator (`func() interface{} { return &X{Port: 443} }`). Validation goes in the tunnel's `NewClient`/`NewServer`, NOT here.
- Config keys are `json:"lower_snake"` style; YAML uses the same tags (YAML loader is `ghodss/yaml` which converts YAML→JSON→struct).

## Anti-patterns

- Don't `json.Unmarshal` raw bytes again inside a tunnel — the context already has your struct.
- Don't share config structs between tunnels — even if fields overlap, separate registrations decouple release cycles.
- Don't mutate config after `NewClient/NewServer` — other goroutines hold the same pointer.
