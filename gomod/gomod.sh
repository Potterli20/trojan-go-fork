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
QUIC_GO_MODULE="github.com/apernet/quic-go"
QUIC_GO_FORK_REPO="HyNetworks/quic-go"
QUIC_GO_FORK_TAG="v0.60.0-mod-rename"

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
# 解法：使用 HyNetworks/quic-go fork 上的 "*-mod-rename" tag，这些 tag 将
# go.mod module 声明保留为 github.com/apernet/quic-go，同时版本与 apernet
# 同步。通过 require + replace 的形式引入：
#   require github.com/apernet/quic-go  v0.60.0-mod-rename
#   replace github.com/apernet/quic-go  v0.60.0-mod-rename => github.com/HyNetworks/quic-go v0.60.0-mod-rename
# ---------------------------------------------------------------------------

update_quic_go() {
    local mod="$QUIC_GO_MODULE"
    local fork_repo="$QUIC_GO_FORK_REPO"
    local fork_tag="$QUIC_GO_FORK_TAG"
    local version="$fork_tag"   # 在 require / replace 中使用的版本号 = fork 的 tag 名

    log_info "quic-go → switching to HyNetworks fork tag $fork_tag (module path kept as $mod)"

    # 3a. 清理所有旧的 quic-go replace 规则（apernet 旧 tag、旧伪版本等）
    # 先枚举所有已存在的 replace，再逐个 drop，避免正则误删
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

    # 3b. 写入新 replace：$mod @ $version  =>  $fork_repo @ $fork_tag
    go mod edit -replace="${mod}@${version}=${fork_repo}@${fork_tag}"

    # 3c. go get 指定版本（replace 已存在，go 会 follow replace 去 fork 仓库拉代码）
    if GOFLAGS="-mod=mod" go get "${mod}@${version}" 2>/tmp/gomod_quic_err; then
        log_ok "quic-go → pinned to HyNetworks/$fork_repo@$fork_tag (imported as $mod)"
    else
        err_msg=$(cat /tmp/gomod_quic_err 2>/dev/null || echo "(no error output)")
        log_error "quic-go → failed to upgrade to $version — ${err_msg//$'\n'/ | }"
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
