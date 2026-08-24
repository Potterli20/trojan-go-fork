#!/bin/bash
set -euo pipefail

# 出错时打印错误位置
trap 'echo -e "\n[FATAL] Script failed at line $LINENO: $BASH_COMMAND" >&2' ERR

# ---------------------------------------------------------------------------
# 配置区：需要跟踪最新 commit / 指定 tag 的依赖
# 格式："go_module_path|github_owner/repo|ref_spec"
#   ref_spec = HEAD                  → 跟踪仓库默认分支的最新 commit（生成伪版本）
#   ref_spec = refs/heads/<branch>   → 跟踪指定分支最新 commit
#   ref_spec = refs/tags/<tag>       → 锁定指定 tag（不跟踪）
# ---------------------------------------------------------------------------
MODULES=(
    "github.com/database64128/tfo-go/v2|database64128/tfo-go|HEAD"
    "github.com/andybalholm/brotli|andybalholm/brotli|HEAD"
    "github.com/xtls/xray-core|XTLS/Xray-core|HEAD"
    "gorm.io/gorm|go-gorm/gorm|HEAD"
    "github.com/refraction-networking/utls|refraction-networking/utls|HEAD"
    "github.com/google/uuid|google/uuid|HEAD"
    "github.com/smartystreets/goconvey|smartystreets/goconvey|HEAD"
    "github.com/gopherjs/gopherjs|gopherjs/gopherjs|HEAD"
    "github.com/Potterli20/socks5-fork|Potterli20/socks5-fork|HEAD"
    "github.com/xtaci/smux|xtaci/smux|HEAD"
)

# quic-go 特殊配置（需要 replace，见下方 update_quic_go 函数）
# 注意：HyNetworks/quic-go 上的 *-mod-rename 是分支（branch）不是 tag，不能
# 直接作为 module version 写进 go.mod（CI 清 modcache 后会报 "is not a tag"）。
# 脚本里会自动 resolve 该分支最新 commit → 生成占位伪版本 → go get 触发 Go 自
# 动计算正确伪版本后写入 go.mod / replace。
QUIC_GO_MODULE="github.com/apernet/quic-go"
QUIC_GO_FORK_REPO="HyNetworks/quic-go"
QUIC_GO_FORK_BRANCH="refs/heads/v0.60.0-mod-rename"

# 需要从"批量 go get -u"中排除的模块路径（quic-go 有特殊处理，避免被乱升级）
EXCLUDE_MODULES_REGEX='^github\.com/(apernet|quic-go)/quic-go$'

# ---------------------------------------------------------------------------
# 工具函数
# ---------------------------------------------------------------------------

# 通过 git ls-remote 获取 GitHub 仓库指定 ref 的 commit hash
# 用法：get_commit_sha "owner/repo" "ref"    ref 可以是 HEAD / refs/heads/* / refs/tags/*
# 成功输出 40 位 hash，失败返回非零
get_commit_sha() {
    local repo="$1"
    local ref="$2"
    local url="https://github.com/${repo}.git"
    local sha

    sha=$(git ls-remote --exit-code "$url" "$ref" 2>/dev/null | awk '{print $1; exit}')
    if [[ -z "$sha" || "${#sha}" -ne 40 ]]; then
        echo "ERROR: cannot resolve $repo @ $ref (got: $sha)" >&2
        return 1
    fi
    echo "$sha"
}

# 彩色/前缀日志
log_info()  { echo "[INFO]  $*"; }
log_ok()    { echo -e "[ \033[32mOK\033[0m ]  $*"; }
log_warn()  { echo -e "[\033[33mWARN\033[0m]  $*" >&2; }
log_error() { echo -e "[\033[31mERR\033[0m ]  $*" >&2; }

# ---------------------------------------------------------------------------
# 步骤 1：升级所有其他直接依赖（排除 quic-go 系列 + 上面单独跟踪的模块）
# ---------------------------------------------------------------------------

log_info "Collecting direct dependencies..."

# 构建单独跟踪模块的排除正则（除了 quic-go，上面 MODULES 里的也不要批量 go get -u，
# 因为我们要精确控制它们的版本来源）
TRACKED_REGEX=""
for entry in "${MODULES[@]}"; do
    mod_path="${entry%%|*}"
    # 转义点号，拼成 | 分隔的正则
    TRACKED_REGEX+="${mod_path//./\\.}|"
done
TRACKED_REGEX+="$EXCLUDE_MODULES_REGEX"

# 收集直接依赖：排除 Main / Indirect / quic-go 系列 / MODULES 中单独跟踪的
readarray -t DIRECT_DEPS < <(
    go list -m -f '{{if and (not .Main) (not .Indirect)}}{{.Path}}{{end}}' all 2>/dev/null \
        | grep -vE "^($TRACKED_REGEX)$" || true
)

if [[ ${#DIRECT_DEPS[@]} -gt 0 ]]; then
    log_info "Upgrading ${#DIRECT_DEPS[@]} untracked direct dependencies via 'go get -u'..."
    GOFLAGS="-mod=mod" go get -u "${DIRECT_DEPS[@]}" || log_warn "Some direct dependencies failed to upgrade (continuing)"
else
    log_info "No extra direct dependencies to upgrade (all are explicitly tracked)."
fi

go mod tidy -compat=1.27

# ---------------------------------------------------------------------------
# 步骤 2：按 MODULES 配置逐个精确升级（追最新 commit 或指定 tag）
# ---------------------------------------------------------------------------

log_info "Processing explicitly tracked modules..."

for entry in "${MODULES[@]}"; do
    mod_path="${entry%%|*}"
    repo="${entry#*|}"; repo="${repo%%|*}"
    ref_spec="${entry##*|}"

    # 解析目标版本：tag → 用 tag 名；分支/HEAD → 拿 commit hash
    if [[ "$ref_spec" == refs/tags/* ]]; then
        target="${ref_spec#refs/tags/}"
        log_info "$mod_path → pinned tag $target"
    else
        if ! target=$(get_commit_sha "$repo" "$ref_spec"); then
            log_warn "$mod_path: skip (failed to get ${ref_spec} commit from $repo)"
            continue
        fi
        log_info "$mod_path → latest commit $target ($repo ${ref_spec/refs\/heads\//})"
    fi

    # 执行 go get；失败只 warn 不终止（单模块失败不应阻塞整个脚本）
    if GOFLAGS="-mod=mod" go get "${mod_path}@${target}" 2>/tmp/gomod_go_get_err; then
        log_ok "$mod_path → upgraded to $target"
    else
        err_msg=$(cat /tmp/gomod_go_get_err 2>/dev/null || echo "(no error output)")
        log_warn "$mod_path: failed to upgrade to $target — ${err_msg//$'\n'/ | }"
    fi
done

# ---------------------------------------------------------------------------
# 步骤 3：quic-go 特殊处理
#
# 背景：apernet/quic-go 从 v0.58+ 正式 tag 起，go.mod module 声明已改为
# github.com/quic-go/quic-go，而项目代码 + xray-core 子包 import 仍使用
# github.com/apernet/quic-go/*，直接 go get 任何新 tag 都会触发
# "module declares its path as ... but was required as ..." 错误。
#
# 解法：使用 HyNetworks/quic-go fork 上的 *-mod-rename 分支（注意是 branch
# 不是 tag！），这些分支的 go.mod module 声明保留为 github.com/apernet/quic-go，
# 代码与 apernet 上游对应正式版本一致。
#
# 难点：Go module version 只能是 tag 或伪版本（pseudo-version），不能直接写
# branch 名 —— 本地有缓存时虽然能解析，但 CI 执行 go clean -modcache 后重新
# 解析会报 "vX.Y.Z-mod-rename is not a tag" 错误。
#
# 因此这里通过以下流程自动转写为伪版本：
#   1) git ls-remote 拿 fork 上 mod-rename 分支的 HEAD commit hash
#   2) 写一个「占位 replace」：把 $mod 全版本替换到 fork 的占位伪版本
#      （base version + timestamp 任意写，hash 部分必须是真实 commit 前 12 位）
#   3) go get $mod@<hash> 触发 Go 去 fork 仓库解析该 commit
#   4) Go 会自动把占位伪版本替换为「基于真实 base tag + commit 时间 + hash」的
#      正确伪版本，写入 require / replace 两端
# ---------------------------------------------------------------------------

update_quic_go() {
    local mod="$QUIC_GO_MODULE"
    local fork_repo="$QUIC_GO_FORK_REPO"
    local fork_branch="$QUIC_GO_FORK_BRANCH"
    local fork_url="https://github.com/${fork_repo}.git"

    log_info "quic-go → resolving HyNetworks/$fork_repo ${fork_branch#refs/heads/} branch HEAD"

    # 3a. 清理所有旧的 quic-go replace 规则（apernet 旧 tag、旧伪版本等）
    local existing_versions
    existing_versions=$(go list -m -json all 2>/dev/null \
        | python3 -c "
import sys, json
data = json.load(sys.stdin)
seen = set()
for m in data.get('Replace', []):
    if m.get('Old', {}).get('Path') == '$mod':
        v = m['Old'].get('Version', '')
        if v and v not in seen:
            seen.add(v)
            print(v)
" 2>/dev/null || true)

    for old_v in $existing_versions; do
        go mod edit -dropreplace="${mod}@${old_v}"
        log_info "quic-go → dropped stale replace ${mod}@${old_v}"
    done
    # 如果存在不带版本的 replace（替换所有版本），也一并清理
    go mod edit -dropreplace="${mod}" 2>/dev/null || true

    # 3b. git ls-remote 拿 branch HEAD commit hash
    local full_sha short_sha
    full_sha=$(git ls-remote --exit-code "$fork_url" "$fork_branch" 2>/tmp/gomod_quic_ls_err \
        | awk '{print $1; exit}')
    if [[ -z "$full_sha" || "${#full_sha}" -ne 40 ]]; then
        err_msg=$(cat /tmp/gomod_quic_ls_err 2>/dev/null || echo "(empty response)")
        log_error "quic-go → cannot resolve branch $fork_branch on $fork_repo — $err_msg"
        return 1
    fi
    short_sha="${full_sha:0:12}"
    log_info "quic-go → branch HEAD commit = $full_sha ($short_sha)"

    # 3c. 写占位 replace（不带 @v 左边 = 替换所有版本）
    #   replace $mod => $fork_repo v0.0.0-00000000000000-$short_sha
    # Go 会在 go get / tidy 时发现该 hash，重新计算正确 base version + timestamp，
    # 并把占位版本替换为真实伪版本写入 go.mod。
    local placeholder="v0.0.0-00000000000000-${short_sha}"
    go mod edit -replace="${mod}=${fork_repo}@${placeholder}"
    log_info "quic-go → wrote placeholder replace (${short_sha}), waiting for Go to resolve pseudo-version"

    # 3d. go get @commit_hash 触发解析。由于 replace 生效，Go 会去 fork 仓库找
    # 该 commit，然后生成正确伪版本写入 require / replace。
    if GOFLAGS="-mod=mod" go get "${mod}@${short_sha}" 2>/tmp/gomod_quic_err; then
        # 3e. go mod tidy 再跑一次，确保 replace 右边的占位伪版本也被修正为正确值
        go mod tidy -compat=1.27 2>/dev/null || true
        local resolved_ver
        resolved_ver=$(go list -m -f '{{if .Replace}}{{.Replace.Version}}{{else}}{{.Version}}{{end}}' "$mod" 2>/dev/null || echo "?")
        log_ok "quic-go → pinned to HyNetworks/$fork_repo pseudo-version $resolved_ver (imported as $mod)"
    else
        err_msg=$(cat /tmp/gomod_quic_err 2>/dev/null || echo "(no error output)")
        log_error "quic-go → go get @${short_sha} failed — ${err_msg//$'\n'/ | }"
        return 1
    fi
}

update_quic_go

# ---------------------------------------------------------------------------
# 步骤 4：最终 tidy，保证 go.mod / go.sum 一致
# ---------------------------------------------------------------------------
log_info "Final go mod tidy..."
go mod tidy -compat=1.27

log_ok "Done. Run 'go list -m all | grep -E '(apernet|quic-go)/quic-go' to verify quic-go version."
