#!/bin/bash
set -e

# 更新所有依赖到最新版本
go get -u ./...

# 整理模块并确保与 Go 1.26 兼容
go mod tidy -compat=1.26

# 获取最新 commit hash 的函数
get_latest_commit() {
    local repo=$1
    curl -s "https://api.github.com/repos/$repo/commits" | grep -m 1 '"sha"' | cut -d '"' -f 4
}

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

# 最后再次整理模块，确保 go.sum 与 go.mod 一致
go mod tidy -compat=1.26
