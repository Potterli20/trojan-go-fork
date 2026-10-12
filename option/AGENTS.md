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
PopOptionHandler() (Handler, error)   // 每次取 Priority 最高的一个，并把它从注册表里删除
GetHandler(name) (Handler, bool)
HandlerCount() int
```

## 三种返回值语义（2026-10-12 才真正成立）

`main.go` 的 `selectAndRun` 按优先级逐个试 handler，只有三种结局：

- `nil` ⇒ 本 handler 接管了这次运行，循环结束，进程正常退出。
- `option.NotSet()` ⇒ 「命令行里没有我要的参数」，继续试下一个。
- 其它错误 ⇒ 参数确实是它的、但执行失败，**立刻**把原因返回给 main 并 `log.Fatal`，
  不再往下试。

判定用的是错误文本（`option.IsNotSet` 比较 `err.Error() == "not set"`），不是
`errors.Is`——本项目的 `common.Error` 用 `" | "` 拼接且没有 `Unwrap` 链，一旦把
`NotSet()` 再 `.Base()` 一层就识别不出来。所以：**handler 必须直接
`return option.NotSet()`，不能包装。**

> 这条约定以前只是文档：`main.go` 从不比较错误文本，任何错误都「试下一个」。
> 后果是一份坏配置会一路落到队尾，用户只看到 `Invalid options - no valid handler found`，
> 真正的原因（JSON 解析失败、地址不合法……）被降级成中间一行 WARN。
> 而且实测 HEAD 上 5 个 handler 里有 4 个在参数缺省时返回的都不是 `"not set"`
> （`easy`→`empty`、`URL`→`url is empty`、`api`→**空字符串**、`PROXY_STDIN`→
> `reading from stdin is disabled`）。现在由 `main_test.go` 的
> `TestEveryHandlerSaysNotSetWhenItsFlagsAreAbsent`（带 HandlerCount 下限自证）钉住。

## Registered handlers

存在性随 build tag 变（`api`/`easy`/`URL` 在 `mini` 里没有）：

| Name | Priority | 触发条件 |
|---|---|---|
| `api` (`api/control`) | 50 | `-api set/get/list/record` |
| `easy` | 50 | `-server` / `-client` + `-password` |
| `version` | 10 | `-version` |
| `URL` | 10 | `-url`（分享链接）|
| `PROXY_STDIN` | 0 | `-stdin-format json/yml`（默认 `disabled` ⇒ NotSet）|
| `PROXY` | **-1** | `-config`，或在当前目录找 `config.json|yml|yaml`（兜底，必须最低）|

没有 `-test`、也没有 `-info` 这两个 handler（旧文档里的那份清单是错的）。

## Conventions

- 参数没被命令行命中时，**只能** `return option.NotSet()`。
- Priority 是 `int`，高的先跑；兜底的 `PROXY` 必须最低（现在 -1，`PROXY_STDIN` 是 0）。
- Handlers 在 `init()` 里用包级 `flag.*` 注册参数——这样 `-h` 才会列出它们。
- 注意 `api`(50) 与 `easy`(50) 优先级相同，而 `PopOptionHandler` 从 map 取值，
  并列时先后顺序不确定；两者参数互斥所以今天无害，别再往这个档位上加 handler。

## Anti-patterns

- Don't add a handler that returns `nil` unconditionally — it becomes a black hole, no other handler ever runs.（`api` 就曾经因为「失败也 return nil」让退出码永远是 0，已由 `TestAPIHandlerReportsFailureInsteadOfPretendingSuccess` 看住。）
- Don't parse `os.Args` manually; always go through the `flag` package so `-h` stays consistent.
