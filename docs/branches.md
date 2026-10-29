# 分支、CI 与移植状态

面向维护者与贡献者。使用者只需要知道一件事：本仓库有 `master` 一个长期分支，
[README](../README.md) 的「项目来源与分支」那点信息就够了。

## 分支

`master` 是唯一长期分支，也是默认分支。功能开发放在本地分支或 PR 分支上完成，合入 `master`
之后就删掉，不留 topic 分支——`git ls-remote --heads origin` 至今只列出 `master` 一个。

2026-10-29 清理时删掉了三条已经合入却留在本地的分支（`docs-config-gate`、`docs-push`、
`fix/addr-append`），它们的提交都还在 `master` 历史里，删掉的只是分支名。

## CI 只认 master

| 工作流 | 触发 | 跑什么 |
| --- | --- | --- |
| `test.yml` | master 的 push / PR | `make test` |
| `config-examples.yml` | master 的 push / PR、手动 | 把文档与 `example/`、`config/` 里的每段配置实喂给真实二进制，再按 (节,键) 核键名，最后重算 Markdown 站内锚点 |
| `docker-build.yml` | master 的 push（只改 `**.md` 时跳过） | 推 Docker Hub 与 ghcr |
| `main.yml` | 每 7 天 + 手动 | 跨平台编译矩阵，产出 Release 与 `V` 标签 |
| `race.yml` | 每日 + 手动 | 全仓 `-race -tags full`；故意不挂 push/PR，否则每 6 小时一次的依赖维护提交会反复触发十来分钟的长跑 |
| `go.yml` | 每 6 小时 + 手动 | 依赖维护后推回 `master` |

`go.yml` 跑的是 `gofmt -w -r 'interface{} -> any'`、`go fix ./...`、`go mod tidy -compat=1.27`、
`gomod/gomod.sh`，然后 `git push -f -u origin master`。因此 `git log` 里 `github-actions[bot]`
的提交数比全部人工提交加起来还多好几倍——按提交数量评估改动量之前先想到这一点。

`config-examples.yml` 的三个分母下限值得知道，因为它们就是"文档里的配置有没有人验证"的护栏：
`config/*.yaml` 至少 5 份、文档里的 ```yaml 段至少 12 段、键对总数至少 150。低于下限直接变红，
避免提取器坏掉时"零未知键"因为什么都没检查而虚假通过。

## 版本标签

发布标签是 `V` 加发布日期（如 `V2026.10.25`），由 `main.yml` 打，至今累积两百多个。

镜像侧 `docker-build.yml` 每次构建都打 `snapshot`（可变别名）与一枚不可变的
`sha-<提交号前 8 位>`；只有 tag 构建才额外打 `latest`。要精确复原某一次构建（包括没成为
正式版的中间提交）必须按 `sha-` 标签或该次构建记在 job summary 里的 digest 取，
`snapshot` 每次 push 都被覆盖。取法见 [README 的取回历史构建产物](../README.md#取回历史构建产物)。

## fregie 各分支还差什么

fregie 的血统与来源链说明见 [README](../README.md#项目来源与分支)。截至 2026-10-29，
fregie 各分支尚未进入本仓库 `master` 的提交数：

| fregie 分支 | 未合入的提交 | 分支最后一次提交 |
| --- | --- | --- |
| `latest`（默认）、`ai-dev`、`memory_optimize`、`websocket_auth` | 0 —— 内容已全在本仓库历史里 | 2024-03 / 2026-06 / 2021-06 / 2021-05 |
| `master` | 1：`cfe8f718` fix upload speedlimit not working | 2021-10 |
| `tls_fix` | 3：`db5453fb` `a24a525b` `76870a2a` | 2021-05 |

上表第一行那四个分支的内容已经全在本仓库历史里——共 46 枚 fregie 提交，例如"指定转发 buffer 大小"
（`9317445e`）、SQLite 改用免 CGO 驱动（`6dc987ce`）、MySQL 删用户后内存不更新（`2a253cbf`）、
`outbound_local_addr` 与 `outbound_fwmark`（`3b5b54b9`、`627d4fbc`），这些就是
[与原版的差异](../README.md#与原版的差异)里对应条目的出处。剩下 4 个提交没有逐字合入，而本仓库
changelog 里"data race""上行限速"两条是同主题独立重做的，两边实现不一定一致——真要引用请以提交号
为准，别假设行为等价。核对方式：

```bash
git remote add fregie https://github.com/fregie/trojan-go.git
git fetch fregie
git log --oneline master..fregie/tls_fix
```
