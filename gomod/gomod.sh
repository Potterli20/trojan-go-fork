#!/bin/bash
set -e

# 获取最新 commit hash 的函数（带校验，失败时返回非零）
get_latest_commit() {
    local repo=$1
    local sha
    sha=$(curl -s --max-time 15 "https://api.github.com/repos/$repo/commits?per_page=1" \
        | grep -m 1 '"sha"' | cut -d '"' -f 4)
    if [[ -z "$sha" || "${#sha}" -ne 40 ]]; then
        echo "ERROR: failed to get latest commit for $repo (sha=$sha)" >&2
        return 1
    fi
    echo "$sha"
}

# 更新所有依赖到最新版本，但排除 quic-go（apernet/quic-go 的新 tag module 声明路径
# 已改为 github.com/quic-go/quic-go，与 require 的 github.com/apernet/quic-go 不一致，
# 必须通过 go.mod 中的 replace 固定到兼容版本，不能直接 go get -u 升级上去）
GOFLAGS="-mod=mod" go get -u $(go list -m -f '{{if not (or (eq .Path "github.com/apernet/quic-go") (eq .Path "github.com/quic-go/quic-go"))}}{{.Path}}{{end}}' all 2>/dev/null) ./...

# 整理模块并确保与 Go 1.27 兼容
go mod tidy -compat=1.27

# 获取 tfo-go 最新的 commit
tfo_commit_hash=$(get_latest_commit "database64128/tfo-go")
go get github.com/database64128/tfo-go/v2@$tfo_commit_hash

# 获取 brotli 最新的 commit
brotli_commit_hash=$(get_latest_commit "andybalholm/brotli")
go get github.com/andybalholm/brotli@$brotli_commit_hash

# 获取 xray 最新的 commit
xray_commit_hash=$(get_latest_commit "XTLS/Xray-core")
go get github.com/xtls/xray-core@$xray_commit_hash

# 获取 gorm 最新的 commit
gorm_commit_hash=$(get_latest_commit "go-gorm/gorm")
go get gorm.io/gorm@$gorm_commit_hash

# 获取 utls 最新的 commit
utls_commit_hash=$(get_latest_commit "refraction-networking/utls")
go get github.com/refraction-networking/utls@$utls_commit_hash

# 获取 uuid 最新的 commit
uuid_commit_hash=$(get_latest_commit "google/uuid")
go get github.com/google/uuid@$uuid_commit_hash

# 获取 goconvey 最新的 commit
goconvey_commit_hash=$(get_latest_commit "smartystreets/goconvey")
go get github.com/smartystreets/goconvey@$goconvey_commit_hash

# 获取 gopherjs 最新的 commit
gopherjs_commit_hash=$(get_latest_commit "gopherjs/gopherjs")
go get github.com/gopherjs/gopherjs@$gopherjs_commit_hash

# 获取 socks5 最新的 commit
socks5_commit_hash=$(get_latest_commit "Potterli20/socks5-fork")
go get github.com/Potterli20/socks5-fork@$socks5_commit_hash

# 获取 smux 最新的 commit
smux_commit_hash=$(get_latest_commit "xtaci/smux")
go get github.com/xtaci/smux@$smux_commit_hash

# quic-go 特殊处理：
# 新 tag 的 go.mod module 声明已改为 github.com/quic-go/quic-go，
# 而 require 路径仍是 github.com/apernet/quic-go，直接 go get 任何新 tag 都会触发
# "module declares its path as ... but was required as ..." 错误。
# 这里只在获取到最新 commit 后，尝试 go get；一旦因 module 路径冲突失败，
# 就保留当前已锁定的兼容伪版本（由 go.mod replace 机制保障），不做强制升级。
quic_go_commit_hash=$(get_latest_commit "apernet/quic-go") || quic_go_commit_hash=""
if [[ -n "$quic_go_commit_hash" ]]; then
    GOFLAGS="-mod=mod" go get github.com/apernet/quic-go@$quic_go_commit_hash 2>/dev/null \
        || echo "WARN: skip quic-go upgrade (newer commit may have incompatible module path), keeping locked version" >&2
fi

# 最后再次整理模块，确保 go.sum 与 go.mod 一致
go mod tidy -compat=1.27
