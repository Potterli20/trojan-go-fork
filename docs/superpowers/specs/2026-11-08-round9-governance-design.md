# 第 9 轮全仓治理设计：bug 修复 + Go 1.27.3 特性应用 + 性能优化

日期：2026-11-08
状态：已经用户批准（范围=最大档；优化=全部都要；交付=分批提交并推送 master）

## 1. 背景与目标

本仓库已经历 8 轮审计/修复（见 AGENTS.md 与各级记忆），本轮是第 9 轮，目标三条：

1. **修复 bug**：先复验第 7 轮遗留队列（约 15 条，部分已被第 8 轮修掉），再做一轮新的全仓只读审计找新缺陷，控制器复验后统一修复。
2. **Go 1.27.3 特性应用**：大头已采用（`wg.Go`、`errors.AsType`、`testing/synctest`、`goroutineleak` 剖析、标准库 `uuid`、MLKEM1024 曲线），本轮做剩余小项扫尾与全仓遗漏扫描。
3. **性能优化**：已知热点全部处理（含两条历史上标记"要动先讨论"的项，本轮用户已批准），另做一轮 benchmark/pprof 驱动的新热点挖掘，量到收益才改。

## 2. 执行策略

采用**只读代理审计 + 控制器统一复验/修复**（第 7/8 轮已验证的形态）：

- 否决"控制器全包串行"：45k 行级全仓审计串行做太慢，只读分片没有共享状态。
- 否决"多写者代理并行修复"：违反本仓库"同一棵工作树只许一个写者"红线（2026-10-16 实测两棒同树互改导致门禁误读）。
- 审计代理只读、不写文件、不在主树跑测试；一棒不超过 5 个关注点（防 150 轮上限截断）。
- 代理报告必须带 file:line 与触发条件；**控制器逐条打开复验后才进修复清单**（防工具结果注入与代理幻觉，历史上有伪造 file:line 的先例）。

## 3. 阶段划分与验证点

```text
阶段0 基线   → pull --rebase 同步 CI 推送；跑 make test（CI 原命令，无标签）
               与 SHADOWSOCKS_SF_CAPACITY=-1 go test -count=1 -tags full ./...，
               落盘存档基线失败清单（HEAD 可能本就红，先归属清楚）
阶段1 复验   → 控制器逐条打开第 7 轮遗留项的当前代码核对，
               已修的划掉并记交叉印证，产出确认清单（含 file:line 证据）
阶段2 新审计 → 6 路只读代理分片扫全仓，控制器复验，合并进确认清单
阶段3 修复   → 确认清单按缺陷等级 P1→P2 排序、按主题分批；每条先写门禁
               （在 pristine-HEAD detached worktree 变异证红）再修，单写者
阶段4 特性   → 第 5 节清单的实际调用点核查与替换，全仓再扫一遍遗漏
阶段5 优化   → 第 6 节清单，先测量后改，前后基准对比落盘
阶段6 交付   → make test + make test-race + 配置键/锚点门禁全绿；
               按主题分批中文提交；pull --rebase 后 SSH 推 master
```

阶段顺序的依赖关系：阶段 1 与阶段 2 可并行（都是只读）；阶段 3 依赖阶段 1+2 的确认清单；
阶段 4 独立但排在修复后以免冲突；阶段 5 依赖阶段 3 完成（优化不改行为，要在行为正确的
基线上量）；阶段 6 收口。

## 4. Bug 工作流细则

### 4.1 第 7 轮遗留队列（复验对象，非既成事实）

来源：项目记忆 `audit-round7-remaining-queue.md`（2026-10-27 快照）。已知其中
simplesocks 串行读头（已改 worker 池）、socks 投递锁外（已修）、adapter 无退避（已修）
等被第 8 轮覆盖，复验时逐条核对当前代码，已修的划掉。

待复验的重点项（P1）：

- `tunnel/websocket/server.go` AcceptConn 在 accept 循环体里串行跑 `http.ReadRequest`，
  静默对端独占入口至握手超时；`trackPending` 晚于 ReadRequest，Close 叫不醒读阶段。
- `tunnel/shadowsocks/server.go` AEAD 密文探测（probeTimeout 10s）在循环体内，
  由 `trojan/server.go` 串行调用。
- `redirector` 诱饵会话并发数无上限（队列 cap 64 只限排队不限并发）。
- `tunnel/http` 响应方向无字节预算（`ReadResponse` 行长与头字节数无上限），
  解析失败把整行原文带进 Error。
- `tunnel/http` 请求体泵与"下一个请求"共用同一 bufio/BoundedReader，
  请求间串字节 + 撕裂态越界 panic 风险。

P2 择要：freedom forwardDialer 丢 NoDelay/KeepAlive/fwmark/localAddr；转发代理 UDP
socket 不走 ListenConfig.Control；socks 握手期在途连接未登记；adapter 嗅探读不被 ctx
叫醒；recorder.broadcast 的 recover 挂函数级；mux usable 检查与 OpenStream 间
check-then-act；transport 是 leaf 却无 AcceptConnGuarded；router.matchIP 每包每 CIDR
现算掩码（并入优化）；tls 客户端无 ClientSessionCache 导致 reuse_session 空转、
tls 握手不接 ctx；trojan 包级 Auth 单例吞 custom 模式第二节点配置；
proxy 的 signal.Notify 挂在 relay 建完之后；trojan/packet.go 每包 log.Debug 先算参数。

### 4.2 第 9 轮新审计分片（6 路只读代理）

1. 入站层：socks / http / adapter / websocket / shadowsocks / simplesocks / trojan / tls
2. 出站与传输：freedom / router / mux / quic / transport / tproxy / dokodemo
3. 组装与入口：proxy / config / option / url / main / component
4. 后端与旁路：statistic（memory/mysql/sqlite）/ api / recorder / redirector
5. 基础设施：common / log / test 夹具与门禁自身
6. 专项：并发与资源生命周期（池账目、关停排空、ctx 传播、表上限）横切全仓

每路输出：疑点 + file:line + 触发条件 + 建议判据形态。控制器复验时先核对否决清单
（见第 8 节），已否决的同型报告直接驳回。

### 4.3 修复纪律

- 每条修复配**能在未修复代码上变红**的门禁；第一次绿不算绿，变异证伪
  （在 pristine-HEAD detached worktree 上跑，不污染主树）。
- 概率性竞争判据跑 6~10 次报 N/N；计时类判据用 `testing/synctest` 假时钟。
- 端到端断言防恒真：能纯函数化的纯函数化 + 接线结构性门禁（h2 状态码钳制的教训）。
- 池化负载判据钉账目（借出计数回到起点），不钉"有没有复用"（-race 下假失效）。
- 跑 CI 实际用的命令：`make test` 无 `-tags full`，弱构建专属失败要专门跑；
  无此构建对象时 Skip 不 FAIL。

## 5. Go 1.27.3 特性扫尾清单

已采用、只做遗漏扫描：`wg.Go`、`errors.AsType`、`testing/synctest`、
`runtime/pprof` goroutineleak、标准库 `uuid`（api/service 已用）、MLKEM1024 曲线。

本轮处理：

| 项 | 动作 | 验证 |
|---|---|---|
| `net/url.URL.Clone` / `Values.Clone` | 查 url/ 分享链接解析器等实际拷贝点，有手工深拷贝才换 | 行为等价测试 |
| `strings.CutLast` / `bytes.CutLast` | 查 `LastIndex`+切片的同型写法 | 行为等价测试 |
| `http.Server.MaxHeaderValueCount` | 用到 tunnel/http 入站 h2c 服务，顺带响应方向加固 | 超限拒绝的门禁 |
| `testing/synctest.Sleep` | 测试辅助函数里有适用点才换 | 测试全绿 |

明确不动：`encoding/json/v2`（2026-10-27 已实测否决：更慢、大小写敏感语义变化、
兼容选项全开只剩风险）；`crypto/mldsa`（代理无后量子签名使用场景）；
`database/sql` 新 driver 接口（不实现 driver）；`go/types.Hasher`（工具链侧）。

## 6. 优化工作流（先测量后改）

1. **router.matchIP 掩码预计算**：CIDR 掩码在规则解析期算好，每包不再现算。
   基准前后对比（ns/op、B/op、allocs/op），正确性由既有 router 测试 + 新增等价判据兜底。
2. **websocket 握手 buffer 池化**：照 AGENTS.md 池账目口径——Get 计数、Put 过 cap
   闸门、所有出口归还、Close 排空；判据钉账目。历史上标记"要动先讨论"，本轮已批准。
3. **tproxy 回包 socket 缓存**：会话内按目的地缓存已做；剩余半条（按源地址跨会话
   缓存）需引入 TTL 与 NAT 语义，先出小设计再动手，基准驱动。历史上标记"要动先讨论"，
   本轮已批准。
4. **新热点挖掘**：复用 test/scenario 真实栈夹具跑 benchmark + pprof（CPU/heap/
   goroutineleak）；量到收益才改，量不到只出报告。AGENTS.md 已量过"别动"的三处
   分配（Address.ReadFrom 栈数组、首帧拼接缓冲、mux 8 字节帧头）不再复评。

## 7. 交付与协调

- 提交按主题分批：串行握手 / http 加固 / redirector / P2 杂项（按模块分）/
  特性扫尾 / 优化各主题 / 新审计修复 / AGENTS.md 更新。中文提交信息、heredoc、
  无签名旁路标志；生成与提交串退出码（&&）。
- 每次提交前重查 `git branch --show-current` 与 `git status`；主树有 UU/MERGE_HEAD
  时绝不提交。CI 每 6 小时推依赖，push 前 `pull --rebase`，禁 force；
  同名不同内容的重复提交按记忆口径识别（逐对 diff --stat），不盲目重放。
- push 走 SSH 一次性地址；推后 ls-remote 独立核验远端尖端。
- AGENTS.md 随本轮结论更新（新否决项、新判据教训），保持"防重踩"知识库角色。
- 本 spec 位于 docs/ 递归 glob 内：不使用 yaml/json 围栏、不使用站内锚点链接，
  避免触发配置键与锚点门禁。

## 8. 明确不动项（否决清单，审计代理同型报告直接驳回）

- `encoding/json/v2` 不采纳（已实测）。
- 模块级 `enabled` 一律不生效属既有设计，不自作主张清理。
- `statistic/memory.AuthUserWithPassword`、`common.StickyWriter` 死代码只报告不删。
- mux 客户端断开的 `invalid protocol` 日志不降级（它是干净拆会话唯一留下的痕迹）。
- `if !timer.Stop() { <-timer.C }` 不是死锁（Go 1.23+ 语义，无人接收时过期 Stop 返回 true）。
- quic"并发 UDP 会话抢数据报"不成立（trojan 的 UDP 走专用 stream）。
- 三处已量过的分配不优化（见第 6 节第 4 条）。
- `recorder.SetCapacity` 配置自伤只记账不改；socks CONNECT 提前回成功包属设计改动，
  本轮不动（要动需先决定"谁来等拨号结果"）。

## 9. 风险

- **审计代理报告质量**：历史上出现过伪造 file:line 与恒真判据，控制器复验是硬门禁。
- **并行会话/CI 推送**：主树可能被并行会话切分支或 CI 推新依赖，每次提交前重查；
  冲突时停下上报，不 force。
- **优化改行为**：所有优化项以既有测试 + 等价判据兜底，基准数据落盘后才合入。
- **会话长度**：实现与证伪分棒、一棒不超过 5 项，触顶前自提带当前状态的 wip。