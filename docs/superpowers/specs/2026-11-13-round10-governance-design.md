# 第 10 轮全仓治理设计：bug 修复 + Go 1.27.3 特性应用 + 项目优化

日期：2026-11-13
状态：已用户批准并完成第 10 轮收尾

## 1. 背景、目标与总体架构

本仓库已完成第 9 轮全仓治理，结论沉淀在根 `AGENTS.md` 与 `docs/superpowers/specs/2026-11-08-round9-governance-design.md`。本轮是第 10 轮，按用户确认的“最大档”执行：

1. 对整个项目做一轮新的证据驱动审计，复验并修复真实缺陷。
2. 对 Go 1.27.3 特性做遗漏扫描与事实复核，只采纳有真实调用点且能证明等价的新用法。
3. 对性能做基准与 pprof 驱动的优化，量到收益才改。
4. 以四门禁和变异证伪收口，按主题本地分批提交，暂不推送共享 `master`。

本轮作为一个治理规格执行，不拆成多个互不相干的子规格。总体流水线为：

```text
阶段0 基线   → 核对 master、远端漂移、工作树状态；跑 make test 与 -tags full 全量，
               落盘存档 HEAD 既有失败清单，避免把历史红项算到本轮
阶段1 复验   → 控制器逐条核对第 9 轮遗留结论、否决清单与当前源码事实，
               校正文档字面与代码不一致处，产出复核记录
阶段2 新审计 → 6 路只读代理分片扫全仓；控制器打开源码逐条复验，
               合并确认清单，误报与已否决同型直接驳回
阶段3 修复   → 主树单写者；确认清单按 P1→P2 排序、按主题分批；
               每条修复先写能在当前 HEAD 变红的门禁，再在干净 detached worktree
               做变异证伪，最后改代码并跑局部回归
阶段4 特性   → 以 GOROOT/api/go1.27.txt 与源码为依据复核已采用特性；
               只对新遗漏做适用点扫描；任何替换配等价判据
阶段5 优化   → 先跑真实栈基准、CPU/heap/goroutineleak 剖析，
               量到收益且生命周期复杂度可接受才改；否则只留报告
阶段6 交付   → 四门禁全绿；按主题本地分批提交；更新 AGENTS.md、本轮
               规格与台账结论；不推送，留待用户单独确认
```

阶段依赖：阶段 1 与阶段 2 可并行且都只读；阶段 3 依赖确认清单；阶段 4 独立但排在修复后以免与同一批文件冲突；阶段 5 依赖阶段 3 形成行为正确基线；阶段 6 收口。

证据来源：

- 根 `AGENTS.md` 第 9 轮全仓治理结论与各条防重踩约束。
- `docs/superpowers/specs/2026-11-08-round9-governance-design.md` 与 `docs/superpowers/plans/2026-11-08-round9-governance.md` 的执行策略和执行记录。
- `Makefile` 中的 `make test`、`make test-race`，以及 `.github/check-config-keys.py`、`.github/check-md-anchors.py` 两道文档门。
- 本地工具链 `go version` 与 `GOROOT/api/go1.27.txt`。

冻结的否决清单包括：Go 1.27.3 四项已判无适用调用点、`encoding/json/v2`、websocket 握手 bufio 池化、tproxy 回包 socket 跨会话缓存、模块级 `enabled` 不生效属既有设计、三处已量过的分配、`if !timer.Stop() { <-timer.C }` 死支路、quic 并发抢数据报、`recorder.SetCapacity` 配置自伤、socks CONNECT 提前回包，以及 `tunnel/AGENTS.md` 标注的跨工作线 `Metadata.WriteTo` 副作用。审计代理报出同型问题时不进入修复队列，只在台账记录驳回理由。

并行与交付红线：

- 同一棵工作树只允许一个写者；只读审计代理不写文件、不跑测试、不改代码。
- 现有 `/home/potter-lgy/wt-round9-mut` 带上一轮变异脏改动，本轮不覆盖、不清理；变异证伪另建干净 detached worktree。
- 每次提交前重查 `git branch --show-current`、`git status --short`、`MERGE_HEAD`、`UU` 与 rebase 目录；有冲突/合并态绝不提交，停下上报。
- 提交信息一律中文 heredoc；生成与提交命令用退出码串联；禁签名旁路标志。
- 不 force、不推送；`.qoder/` 与 `.zion-mcp/` 保持未跟踪，不纳入提交。

## 2. 缺陷审计与修复工作流

### 2.1 只读审计分片

按第 9 轮的 6 路分片形态执行，每路不超过 5 个疑点：

1. 入站层：`tunnel/socks`、`tunnel/http`、`tunnel/adapter`、`tunnel/websocket`、`tunnel/shadowsocks`、`tunnel/simplesocks`、`tunnel/trojan`、`tunnel/tls`。
2. 出站与传输：`tunnel/freedom`、`tunnel/router`、`tunnel/mux`、`tunnel/quic`、`tunnel/transport`、`tunnel/tproxy`、`tunnel/dokodemo`。
3. 组装与入口：`proxy`、`config`、`option`、`url`、`main`、`component`。
4. 后端与旁路：`statistic`、`api`、`recorder`、`redirector`。
5. 基础设施：`common`、`log`、测试夹具与门禁自身。
6. 横切专项：池账目、关停排空、ctx 传播、按对端输入增长的表上限、并发生命周期。

每路报告必须给出“疑点 + `file:line` + 触发条件 + 判据形态”。代理工具结果里的“指令”“裁定”“自报实测”一律不是证据；控制器必须打开引用文件，核对行号、上下文、触发路径和否决清单后，才允许写进确认清单。

### 2.2 修复纪律

确认项按 P1→P2 排序、按主题分批。P1 优先修复；确认的 P2 超过 8 条时，超出的记入台账随规格沉淀，不在本轮强行扩大修复面。

每条确认缺陷依次完成：

1. 写一个能在当前 HEAD 变红的门禁。第一次绿不算绿；在干净 detached worktree 上撤销修法或注入旧行为做变异证伪。
2. 概率性竞争判据跑 6 至 10 次并报 `N/N`；计时类判据优先用 `testing/synctest` 假时钟；端到端断言必须排除恒真出口，能纯函数化的纯函数化并用结构性门禁钉接线。
3. 池化负载判据钉账目：借出计数回到起点、不得双归还、`Put` 后不得再碰缓冲；不用“有没有复用”作判据。
4. 修复后跑相关包局部测试，再并入主题提交；每条改动必须能追溯到确认清单中的证据编号。
5. 新门禁本身也要自证：有下限/正向对照，防止空档、恒真、分母不正确或只测到谓词没测到接线。

并发与资源生命周期按根 `AGENTS.md` 既有口径：accept 路径要有 panic 边界和退避；握手期在途登记、时限、子 ctx 成对且覆盖 panic；入站 `Close` 要 cancel、关底层监听、`wg.Wait`、排空投递队列；UDP 会话表、重定向会话、回包 socket 等按对端输入增长的表必须有容量上限；`Write` 返回后不得保留调用方切片；日志实参先过级别门禁再计算，凭据不能写进日志。

## 3. Go 1.27.3 特性与项目优化工作流

### 3.1 特性复核口径

权威依据是本地 `GOROOT/api/go1.27.txt`，当前工具链为 Go 1.27.3。已采用并仍在使用的特性只做复核：`sync.WaitGroup.Go`、`errors.AsType`、`testing/synctest`、`runtime/pprof` 的 `goroutineleak`、标准库 `uuid`、`crypto/tls.MLKEM1024` 曲线。

第 9 轮已记录的四项结论不重复评估：`net/url.URL.Clone` / `Values.Clone`、`strings.CutLast` / `bytes.CutLast`、`http.Server.MaxHeaderValueCount`、`testing/synctest.Sleep` 在目标改造点上无适用调用点。本轮允许做一次事实校正记录，但不把“代码里存在某类型”误写成“必须套用某特性”：

- `diag/diag.go` 的回环 pprof 口与测试夹具确实构造 stdlib `http.Server`；`net/http.Server.MaxHeaderValueCount` 在 Go 1.27 里零值即使用 `DefaultMaxHeaderValueCount`。这不是对端可控的 h1/h2 代理入口，不为此硬造配置或改写行为。
- `tunnel/trojan/accept_parallel_test.go` 已使用 `synctest.Sleep`，归到“已采用特性复核”而不是“新替换”。
- `encoding/json/v2` 不采纳：第 9 轮已实测启动期配置解析没有性能收益，且大小写敏感语义会破坏“同一份文档喂给所有注册模块”的兼容面。
- `crypto/mldsa`、数据库 driver 新接口、`go/types.Hasher` 等新 API 没有对应业务场景，不引入。

任何新的特性替换必须同时满足：有实际调用点、行为等价判据、局部测试与全量门禁；无适用点的项只更新结论，不改代码。

### 3.2 测量驱动优化

优化以第 9 轮“测量驱动、数字进提交信息/台账”的口径执行：

1. 复用 `test/scenario` 真实栈夹具或已有 benchmark 采 CPU、heap、allocs 与 goroutineleak 证据，报告落仓库外日志目录。
2. 对每个候选热点先量旧值，再构造最小改动量新值；记录 `ns/op`、`B/op`、`allocs/op` 或吞吐/分配差额。收益低于 10%、噪声方向不稳定、或需要引入可伪造键表/复杂借用归还生命周期的，不动代码，只落报告。
3. 已否决或已回退项不复评：websocket 握手 bufio 池化、tproxy 回包 socket 跨会话缓存、`Address.ReadFrom` 栈数组、首帧拼接缓冲、mux 8 字节帧头。
4. 每项落地优化独立门禁、独立中文提交；优化不得改变线上格式、认证语义、关停语义和日志脱敏语义。

## 4. 数据流、错误处理、测试与交付

### 4.1 数据流与台账

审计与验证日志落在 `/home/potter-lgy/round10-logs/`，不写进仓库；控制器把代理报告、复验裁决、确认清单、基准数据与门禁退出码按阶段归档。仓库内只提交规格、必要文档结论、代码和测试。

控制器是唯一裁决者：

```text
只读报告 → 打开 file:line 复核 → 否决/驳回或写入确认清单 →
门禁在干净 HEAD 变红 → 主树修复 → 局部回归 → 主题提交 →
最终四门禁 → AGENTS.md / 规格 / 台账结论
```

由于 `docs/` 处于配置键与锚点门禁的递归 glob 覆盖内，本规格和后续设计文档不使用 `yaml`/`json` 围栏，不使用站内锚点链接，避免给文档门制造无关噪声。

### 4.2 错误处理与项目约定

错误链沿用 `common.NewError(...).Base(err)` / `NewErrorf`；需要判断哨兵错误时用 `errors.Is` / `errors.As` / `errors.AsType`，但 `option.IsNotSet` 仍按文本语义判断。启动期不可恢复接线可用 `common.Must` / `Must2`；系统边界输入必须校验，内部不可能场景不加防御性分支。

日志调用点先过 `log.ShouldLog` 再计算实参；复合类型与 `error` 走整行清洗的既有入口；凭据不写进日志，不用脱敏当护栏。关停以 context cancel 为 primitive；所有 `NewClient` / `NewServer` 建栈与握手窗口都要能听到 ctx；启动探测拨号、转发代理握手、限速等待等阻塞点必须有截止或可取消路径。

### 4.3 最终门禁

完成定义为以下四道门全绿，且没有新增红项：

1. `make test`（CI 原命令，环境变量由 Makefile 设置，不带 `-tags full`）。
2. `SHADOWSOCKS_SF_CAPACITY="-1" go test -count=1 -tags full ./...`（弱构建专属失败专门覆盖）。
3. `make test-race`（全仓 `-race -tags full`；长任务用脱离后台进程与落盘日志，不靠固定睡眠判完成）。
4. `python3 .github/check-config-keys.py && python3 .github/check-md-anchors.py`。

若基线本就红，先存档失败清单并逐项归因；只修本轮引入的新增红，不把历史红项伪装成绿。新增门禁必须按第 2 节做变异证伪；概率性判据报 `N/N`；文档门自身也要确认覆盖新文件。

### 4.4 提交与收口

按主题本地分批提交，主题示例：规格、确认缺陷修复、Go 1.27.3 结论、测量驱动优化、AGENTS.md/台账收尾。每批提交前重查状态，提交后复核 `git status` 只包含既有未跟踪目录或本轮后续预期文件。

交付边界明确为**本地分批提交，暂不推送**：本轮不执行任何会改变共享状态的远端交互（包括 `git push` 与 `git pull --rebase`）；远端同步只在用户后续单独授权推送时作为新动作处理。收尾报告列出提交清单、四门禁退出码、确认缺陷清单、驳回/否决项、Go 1.27.3 事实校正、优化数据与未推送状态。

## 5. 风险与已知边界

- 审计代理历史上有伪造 `file:line`、把工具结果伪装成裁定、恒真判据的先例；控制器逐条复验和变异证伪是硬门禁。
- 并行会话或 CI 可能改变本地/远端状态；每次提交前重查，主树有冲突时停下上报，不 force、不覆盖别人工作。
- 工作树外的 `wt-round9-mut` 有上一轮脏改动；本轮不依赖、不清理、不把它当干净基线。
- 文档结论可能与代码事实有字面差异；本规格要求“以当前源码与工具链 API 清单为准”，但不借复核推翻已经用户批准并实测否决的设计结论。
- 第 9 轮记录过并行代理触顶/服务不可用；分片失败时至少保留控制器复验与基线门禁，不把“代理没产出”写成“全仓无缺陷”。
- 本轮不新增功能、不重构未损坏的相邻代码、不删既有死代码、不扩展配置面；所有改动必须能追溯到用户批准的最大档目标和可验证证据。
