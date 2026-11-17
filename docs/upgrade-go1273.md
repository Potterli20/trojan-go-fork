# Go 1.27.3 升级、优化与缺陷修复记录

## 1. 版本与兼容性结论

- 工具链：`go version go1.27.3 linux/amd64`
- `go.mod` 声明：`go 1.27.3`
- 依赖链：`go mod tidy` 无差异；xray-core 要求的 quic-go 已由 `replace` 指向兼容 Go 1.27 的版本。未发现需要仅为适配 Go 1.27 而升级的依赖。
- 结论：项目已在 Go 1.27.3 下完整编译（`go build ./...`）、静态检查（`go vet ./...`）与全量测试通过。

### Go 1.27 新特性的采用原则

本项目不需要为"用新特性而用新特性"。经核实：

- `sync.WaitGroup.Go`（Go 1.25+）已在既有治理中采用，本次新增的并发测试也沿用该方法确保 `Add`/`Done` 配对。
- `container/vector`、`testing/synctest` 等新包在本项目无适用场景，强行引入不会带来收益。
- 手写切片搬移（`append(a[:i], a[i+1:]...)`）已在既往治理中替换为 `slices.Delete`；本次未发现遗留点。
- 本次涉及的新特性应用是：日志热路径改用 `atomic.Int32` / `atomic.Pointer`（标准库自 Go 1.19 起稳定，配合 Go 1.27 内存模型保证无锁读写的正确性），消除运行期调级/重挂输出时的数据竞争。

## 2. 缺陷修复清单

### 2.1 [并发安全] simplelog 日志级别/输出的数据竞争

- 位置：`log/simplelog/simplelog.go`
- 问题：`SimpleLogger.SetLogLevel` / `SetOutput` 在运行期可被 API 调用（配置重载/远程调级），而并发的代理 goroutine 在每条日志里裸读 `logLevel` / `logger`。`golog.Logger` 同类实现用 `mu` + `atomic.Int32` 保护，`simplelog` 却完全无锁，race detector 可捕获该竞争。
- 修复：`logLevel` 改为 `atomic.Int32`，`logger` 改为 `atomic.Pointer[golog.Logger]`；所有写路径 `Store`、读路径先 `Load` 为局部快照再用，避免"检查后使用"之间状态翻转。
- 回归证据：新增 `TestConcurrentLogAndConfig`，8 个 goroutine 并发写日志 + 2 个 goroutine 并发调级/换输出，`-race -count=5` 全绿。

### 2.2 [并发安全/资源] transport Server 首次 Accept 与 Close 的启动竞态

- 位置：`tunnel/transport/server.go`
- 问题：`AcceptConn` 首次调用时通过 `acceptOnce.Do` 向 `wg` 里 `Go(acceptLoop)`；该动作与 `Close` 中的 `closeMu` + `wg.Wait()` 原不共享同一把锁。若关闭路径走到 `wg.Wait()` 之后另一个 goroutine 才首次 `AcceptConn`，就会在 `Wait` 之后 `Add`，构成 WaitGroup 误用，并让新 loop 对已关闭 listener `Accept`。全量 `-race` 下由 `TestSharedAuthenticatorRefcount` 偶发捕获（`sync.(*Once).doSlow` 与关停写竞争）。
- 修复：在 `AcceptConn` 内以 `closeMu` 包住 `closed` 检查与 `acceptOnce.Do`，与 `Close` 的完整关停流程串行；关闭后到达的首次 `AcceptConn` 直接返回错误。
- 回归证据：新增 `TestConcurrentFirstAcceptAndClose`，20 轮 × 8 个并发 `AcceptConn` + `Close`，`-race -count=5` 全绿；隔离的 `TestSharedAuthenticatorRefcount -race -count=50` 全绿、零告警。

### 2.3 [测试判据] WebSocket 重定向用例的概率性假红

- 位置：`tunnel/websocket/handshake_panic_close_test.go`、`handshake_stall_test.go`
- 问题：`TestRedirectedConnIsNotClosedByHandshakeCleanup` 用 `probe.closed` 判断"握手层是否提前关闭被重定向的连接"，但诱饵地址 `127.0.0.1:80` 通常不可达，redirector worker 拨号失败后会 `defer InboundConn.Close()`，同样置位 `probe.closed`。worker 调度时机的差异导致判据随 worker 的调度时机概率性变红（实测 4/50 红）。生产逻辑本身正确：交给 redirector 后握手层的 defer 不会关闭该连接。
- 修复：测试启动一个可达且只 accept、不读不关的诱饵 listener，并等待 redirector 完成中继后再判定，使 `probe.closed` 只反映握手层行为。
- 回归证据：修复前 `-race -count=50` 稳定复现 4 次红；修复后 `-race -count=200` 全绿。另两个用例 `-race -count=20` 全绿。

### 2.4 [代码清理] geodata 缓存的无效 nil 防御

- 位置：`common/geodata/cache.go`
- 问题：`geoipCache.Set` / `geositeCache.Set` 使用值接收者，`if g == nil { g = make(...) }` 只改局部副本，写必然丢失。该分支是永远无效的防御性代码；`NewGeodataLoader` 路径保证 map 非 nil，因此不影响生产结果，但会误导读者。
- 修复：删除无效分支，让 `Set` 的语义与值接收者 map 的真实语义一致。

### 2.5 [测试健壮性] 脆弱的文本断言

- 位置：`tunnel/transport/close_mutex_test.go`
- 问题：`TestCloseUsesMutexState` 对字段声明断言写死了 gofmt 对齐用的空格数，注释变动触发表格重排后即误报。
- 修复：断言前先 `strings.Fields` + `Join` 归一化空白，使检查对格式不敏感。

### 2.6 [逻辑错误] geoip 回退分支的国家码反射恒为空

- 位置：`common/geodata/cache.go`
- 问题：`countryCodeFromGeoIP` 反射查找 `CountryCode` 字段与 `GetCountryCode` 方法，但 xray-core 的 `GeoIP` 真实字段是 `Code`、getter 是 `GetCode`（定义在指针上）。反射实测两个名字都不存在，函数**恒返回空串**，导致 geoip 回退分支的 `EqualFold(code, "")` 永远匹配不上，任何条目都走到 "not found"。对照同文件 `countryCodeFromGeoSite` 直接调用 `GetCode()`，两者写法不对称。
- 修复：改为直接调用 `geoip.GetCode()`（protobuf getter 自带 nil 保护），与 geosite 路径一致；顺带移除无效的 `reflect` 依赖与每条目数次反射调用的开销。
- 回归证据：新增 `TestCountryCodeFromGeoIP`，覆盖大小写保留、空串、nil 四种情形，`-race` 全绿。

### 2.7 [可观测性] 协议短读丢弃底层错误

- 位置：`tunnel/trojan/packet.go`、`tunnel/metadata.go`
- 问题：UDP 包读取的 length / CRLF / payload 三段 `io.ReadFull`，以及域名读取的 length / content 两段，原样丢弃底层错误，只留一句无根因的 `common.NewError(...)`。日志里无法区分 EOF / connection reset / timeout，排障时看不到真实原因；同文件其余 ReadFull 分支都已用 `.Base(err)` 挂载根因，唯独这几处遗漏。
- 修复：五处均补上 `.Base(err)`，控制流完全不变（仍然 return error）。
- 回归证据：新增 `TestPacketReadShortErrorsCarryCause`（3 个子用例）与 `TestReadFromDomainShortErrorsCarryCause`（2 个子用例），断言错误链中可 `errors.Is` 到 `io.ErrUnexpectedEOF` / `io.EOF`，`-race` 全绿。

## 3. 已核查但判定为非缺陷的项

- `tunnel/shadowsocks` AEAD 错误、`test/scenario TestForward` EOF：单独复现均为确定性失败，根因是缺少项目测试标准要求的环境变量 `SHADOWSOCKS_SF_CAPACITY=-1`（go-shadowsocks2 的流容量检查）。加上该变量后 `-count=3` 全绿，Makefile 的 `test` 目标已正确设置，非代码 bug。
- `tunnel/websocket/client.go` 的 `dialCtx`：`websocket.NetConn` 使用 `context.Background()`，不受握手用 `dialCtx` 取消影响，无连接生命周期问题。
- 子代理提出的指纹曲线列表、字符串切分等"优化点"：要么已有治理，要么收益不足且会带来行为/排序变化风险，未采纳。

### 深入优化阶段新增的核查结论

- `common/geodata/cache.go` 未把 `ErrInvalidGeodataLength` 纳入"整文件解析回退"：临时构造"一条长度越界、另有有效条目"的 .dat 实测，回退用的 `proto.Unmarshal` 对含坏条目的文件同样报 wire-format 错误，救不回目标条目；目标在坏条目前时原路径本就成功。纳入该 case 只会多读一次整文件，无实际收益，未采纳。
- `common/geodata/loader.go` 每次加载的 `runtime.GC()`：是 `tunnel/router/client.go` 多次加载后打印 `runtime.MemStats` 的有意配套，移除会改变内存统计口径，属设计选择，未改。
- `common/io.go` `RewindConn` 的 `SetBufferSize(0)` panic / `Rewind` 重复调用：仅当调用顺序异常才会触发，全仓两处调用均为 `Rewind → StopBuffering` 且不再 Rewind，无实际触发路径，不改内部 API 契约。
- `config/config.go` 对未知/类型不匹配字段静默忽略：`AGENTS.md` 明确这是"配置格式兼容"的有意设计（YAML/JSON 字段双向兼容），非缺陷。
- UDP-over-TCP 读取未设读截止时间：经核查该会话在 `Proxy.Close` 时由 `ctx.Done()` 唤醒外层、`defer Close()` 关闭底层连接从而解除 read 阻塞，"关停永久挂起"为误报。

## 4. 验证矩阵

全部命令在 `SHADOWSOCKS_SF_CAPACITY=-1` 环境下执行，使用独立 GOCACHE 避免工具链/缓存污染。

| 验证项 | 命令 | 结果 |
| --- | --- | --- |
| 格式化 | `gofmt -l .` | 无输出 |
| 构建 | `go build ./...` | 通过（exit 0） |
| 静态检查 | `go vet ./...` | 通过（exit 0） |
| 全量单测 | `go test -count=1 ./...` | 39 个测试包全部通过（另 1 包无测试文件），0 FAIL |
| 全量竞态 | `go test -count=1 -race -tags full ./...` | 39 个测试包全部通过，0 FAIL，无 DATA RACE |
| WebSocket 重定向专项 | `-race -count=200` | 通过 |
| transport 并发关停专项 | `-race -count=5`（每轮 20 次循环） | 通过 |
| simplelog 并发专项 | `-race -count=5` | 通过 |
| geoip 国家码反射专项 | `-race` | 通过 |
| 协议短读错误链专项 | `-race`（packet 3 例 + metadata 2 例） | 通过 |
| HTTP 在途响应关停专项 | `-race -count=3` | 通过（关停约 5ms） |
| 性能基准 | `go test -run '^$' -bench . -benchtime=100ms` | 见下表 |

关键性能基线（`-benchtime=100ms`，`-6` 为 GOMAXPROCS；仅供后续回归对比，非优化前后对照）：

| Benchmark | 结果 |
| --- | --- |
| `BenchmarkConnectionTrackerHighConcurrency` | 14852 ns/op |
| `BenchmarkConnectionTrackerDisabledLog` | 7304 ns/op |
| `BenchmarkSanitizeString/secret` | 108.9 ns/op, 0 allocs/op |
| `BenchmarkObfuscateSensitiveData/secret` | 49.96 ns/op, 0 allocs/op |
| `BenchmarkAddressReadFromIPv4` | 55.39 ns/op, 2 allocs/op |
| `BenchmarkStickyConnWrite`（mux） | 20.16 ns/op, 0 allocs/op |
| `BenchmarkMatchIPPrefixes`（router） | 5574 ns/op, 0 allocs/op |

## 5. 变更文件

- `log/simplelog/simplelog.go`、`log/simplelog/simplelog_test.go`
- `tunnel/transport/server.go`、`tunnel/transport/close_mutex_test.go`
- `tunnel/websocket/handshake_panic_close_test.go`、`tunnel/websocket/handshake_stall_test.go`
- `common/geodata/cache.go`、`common/geodata/country_code_test.go`
- `tunnel/trojan/packet.go`、`tunnel/trojan/packet_read_error_test.go`
- `tunnel/metadata.go`、`tunnel/metadata_wire_test.go`
- 本文件 `docs/upgrade-go1273.md`

## 6. 注意事项

- 运行测试必须带 `SHADOWSOCKS_SF_CAPACITY=-1`，或使用 `make test` / `make test-race`，否则 shadowsocks 相关用例会因 go-shadowsocks2 的流容量检查确定性失败。
- 新增的并发回归测试只有带 `-race` 运行才有意义。
- 本次未修改任何依赖版本；`go.mod` / `go.sum` 无变更。
- `test/scenario/TestHTTPShutdownIsNotStalledByInflightResponse` 是全量 `-race` 并行高负载下偶发的时序抖动用例（关停耗时阈值 3s，实测偶发 4.4s；单独或 `-count=3` 重跑约 5ms 且全绿）。该用例的关停路径与本次所有改动均无关，判定为 flaky，非回归。
