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
# QUIC_GO_FORK_BRANCH 留空 = 自动发现版本号最大的 *-mod-rename 分支
QUIC_GO_MODULE="github.com/apernet/quic-go"
QUIC_GO_FORK_REPO="HyNetworks/quic-go"
QUIC_GO_FORK_BRANCH=""  # 留空则自动发现；也可手动指定如 "refs/heads/v0.61.0-mod-rename"

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
# 解法：使用 HyNetworks/quic-go fork 上的 *-mod-rename 分支。这些分支在
# apernet 对应正式 tag 的基础上只改了 go.mod 的 module 声明（保留为
# github.com/apernet/quic-go），代码完全一致。
#
# 难点 1：*-mod-rename 是 branch 名不是 tag，不能直接写进 go.mod 做 version
#         （CI 清 modcache 后 Go 会直接报 "vX.Y.Z-mod-rename is not a tag"）
# 难点 2：只能使用伪版本（pseudo-version），且 Go 对伪版本的 3 个组成部分
#         base version / timestamp / hash-12 都做严格校验，必须与 commit
#         在 fork 仓库中的真实状态完全一致：
#           base version = 该 commit 之前最近一个正式 tag 的 PATCH+1
#           timestamp    = commit 的 UTC 时间（YYYYMMDDHHMMSS）
#           hash-12      = commit hash 前 12 位
#         任何一个字段错（哪怕只是时间戳早了 1 秒）都会被 Go 拒绝。
#
# 因此流程：
#   1) git ls-remote 拿 branch HEAD 的 40 位 commit hash
#   2) GitHub API /repos/:owner/:repo/commits/:sha 拿 commit 的 UTC 提交时间
#   3) 从 branch 名 vX.Y.Z-mod-rename 反推出 base tag vX.Y.Z，
#      base version 取 vX.Y.(Z+1)（mod-rename commit 在 tag 之后）
#   4) 拼成 vBASE-TIMESTAMP-HASH12 伪版本
#   5) 清理所有旧 quic-go replace，写带精确版本约束的 replace，再 go get
# ---------------------------------------------------------------------------

update_quic_go() {
    local mod="$QUIC_GO_MODULE"
    local fork_repo="$QUIC_GO_FORK_REPO"                  # owner/repo 格式（用于 git ls-remote + GitHub API URL）
    local fork_mod="github.com/${QUIC_GO_FORK_REPO}"      # 完整 module path（用于 replace 右边，必须带域名）
    local fork_branch="$QUIC_GO_FORK_BRANCH"
    local fork_url="https://github.com/${fork_repo}.git"

    # 如果 fork_branch 留空，自动发现版本号最大的 *-mod-rename 分支
    if [[ -z "$fork_branch" ]]; then
        log_info "quic-go → auto-discovering latest *-mod-rename branch on ${fork_mod}"
        # 列所有 refs/heads/v*-mod-rename 分支，按版本号排序取最大
        local discovered
        discovered=$(git ls-remote "$fork_url" 'refs/heads/v*-mod-rename' 2>/dev/null \
            | awk '{print $2}' \
            | sed 's|refs/heads/||' \
            | sed 's|-mod-rename||' \
            | sort -V \
            | tail -1)
        if [[ -z "$discovered" ]]; then
            log_error "quic-go → no *-mod-rename branch found on $fork_repo"
            return 1
        fi
        fork_branch="refs/heads/${discovered}-mod-rename"
        log_info "quic-go → latest mod-rename branch = ${discovered}-mod-rename"
    fi

    # branch 名形如 refs/heads/v0.60.0-mod-rename → 取出纯名称 v0.60.0-mod-rename
    local branch_short="${fork_branch#refs/heads/}"
    log_info "quic-go → resolving ${fork_mod} branch ${branch_short}"

    # ------------------------------------------------------------------
    # 步骤 1：git ls-remote 拿 branch HEAD 的完整 40 位 commit hash
    # ------------------------------------------------------------------
    local full_sha short_sha
    full_sha=$(git ls-remote --exit-code "$fork_url" "$fork_branch" 2>/tmp/gomod_quic_ls_err \
        | awk '{print $1; exit}')
    if [[ -z "$full_sha" || "${#full_sha}" -ne 40 ]]; then
        local err_msg
        err_msg=$(cat /tmp/gomod_quic_ls_err 2>/dev/null || echo "(empty response)")
        log_error "quic-go → cannot resolve branch $fork_branch on $fork_repo — $err_msg"
        return 1
    fi
    short_sha="${full_sha:0:12}"
    log_info "quic-go → branch HEAD commit = ${full_sha} (short=${short_sha})"

    # ------------------------------------------------------------------
    # 步骤 2：GitHub API 拿该 commit 的真实提交时间戳（UTC）
    # ------------------------------------------------------------------
    local iso_date timestamp
    iso_date=$(curl -s --max-time 15 "https://api.github.com/repos/${fork_repo}/commits/${full_sha}" 2>/tmp/gomod_quic_api_err \
        | python3 -c 'import sys,json; d=json.load(sys.stdin); print(d["commit"]["committer"]["date"])' 2>/dev/null || true)
    if [[ -z "$iso_date" ]]; then
        local err_msg
        err_msg=$(cat /tmp/gomod_quic_api_err 2>/dev/null || echo "(curl/GitHub API failure)")
        log_error "quic-go → cannot fetch commit date from GitHub API — $err_msg"
        return 1
    fi
    timestamp=$(python3 -c '
from datetime import datetime
import sys
try:
    dt = datetime.fromisoformat(sys.argv[1].replace("Z", "+00:00"))
    print(dt.strftime("%Y%m%d%H%M%S"))
except Exception as e:
    print(f"ERROR: {e}", file=sys.stderr)
    sys.exit(1)
' "$iso_date" 2>/tmp/gomod_quic_ts_err) || {
        local err_msg
        err_msg=$(cat /tmp/gomod_quic_ts_err 2>/dev/null || echo "failed to parse $iso_date")
        log_error "quic-go → $err_msg"
        return 1
    }
    log_info "quic-go → commit UTC date = ${iso_date} (timestamp=${timestamp})"

    # ------------------------------------------------------------------
    # 步骤 3：从 branch 名计算伪版本的 base version
    #   branch 名 = vX.Y.Z-mod-rename
    #   对应 base tag = vX.Y.Z。mod-rename 分支在该 tag 基础上改了 go.mod，
    #   因此该 commit 一定 NOT ON tag，根据 Go 伪版本规则，base = vX.Y.(Z+1)
    # ------------------------------------------------------------------
    local base_tag="${branch_short%-mod-rename}"  # v0.60.0-mod-rename → v0.60.0
    if [[ "$base_tag" == "$branch_short" ]]; then
        log_error "quic-go → branch name '$branch_short' does not match *-mod-rename pattern; cannot infer base tag"
        return 1
    fi
    if [[ ! "$base_tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
        log_error "quic-go → extracted base tag '$base_tag' is not a valid semver tag (vX.Y.Z required)"
        return 1
    fi
    local mmp="${base_tag#v}"                               # 0.60.0
    local major="${mmp%%.*}"                                # 0
    local rest="${mmp#*.}"                                  # 60.0
    local minor="${rest%%.*}"                               # 60
    local patch="${rest#*.}"                                # 0
    local next_patch=$(( patch + 1 ))
    local base_ver="v${major}.${minor}.${next_patch}"       # v0.60.1
    log_info "quic-go → base tag = ${base_tag}, pseudo base version = ${base_ver} (PATCH ${patch} → ${next_patch})"

    # 拼完整伪版本
    # 格式规则（Go 模块参考文档）：
    #   commit 在 release tag vX.Y.Z 之后 → 伪版本 = vX.Y.(Z+1)-0.YYYMMDDHHMMSS-HASH12
    #   其中的 "-0." 前缀不可省，表示这是 (Z+1) 的 pre-release；缺这个前缀 Go
    #   会以 "unknown revision" 拒绝（因为 git 仓库里不存在该字符串作为 ref）。
    local pseudo_ver="${base_ver}-0.${timestamp}-${short_sha}"
    log_info "quic-go → computed pseudo-version = ${pseudo_ver}"

    # ------------------------------------------------------------------
    # 步骤 4：清理旧 replace，写入正确的 require/replace
    # ------------------------------------------------------------------
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
    # 也清理不带版本的 replace（替换所有版本）
    go mod edit -dropreplace="${mod}" 2>/dev/null || true

    # 写带版本约束的 replace（精确版本，不是全量替换，避免意外影响 indirect 依赖的选择）
    go mod edit -replace="${mod}@${pseudo_ver}=${fork_mod}@${pseudo_ver}"
    log_info "quic-go → replace ${mod}@${pseudo_ver}  =>  ${fork_mod}@${pseudo_ver}"

    # go get 精确版本 — 此时 replace 已写好，Go follow replace 拉 fork 仓库，
    # 伪版本与 commit 完全匹配，会通过校验直接写入 require
    if GOFLAGS="-mod=mod" go get "${mod}@${pseudo_ver}" 2>/tmp/gomod_quic_err; then
        # tidy 一次收齐 go.sum
        go mod tidy -compat=1.27 2>/dev/null || true
        local resolved_ver resolved_replace
        resolved_ver=$(go list -m -f '{{.Version}}' "$mod" 2>/dev/null || echo "?")
        resolved_replace=$(go list -m -f '{{if .Replace}}{{.Replace.Path}}@{{.Replace.Version}}{{else}}(no replace){{end}}' "$mod" 2>/dev/null || echo "?")
        log_ok "quic-go → require  ${mod}@${resolved_ver}"
        log_ok "quic-go → →→ replace with ${resolved_replace}"
    else
        local err_msg
        err_msg=$(cat /tmp/gomod_quic_err 2>/dev/null || echo "(no error output)")
        log_error "quic-go → go get @${pseudo_ver} failed — ${err_msg//$'\n'/ | }"
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
