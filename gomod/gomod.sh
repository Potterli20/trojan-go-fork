# 获取 tfo-go 最新的 commits
tfo_commit_hash=$(curl -s https://api.github.com/repos/database64128/tfo-go/commits | grep "sha" | head -n 1 | cut -d '"' -f 4)
# 使用提取的 commit hash 通过 go get 获取 tfo-go
go get github.com/database64128/tfo-go/v2@$tfo_commit_hash

# 获取 brotli 最新的 commits
brotli_commit_hash=$(curl -s https://api.github.com/repos/andybalholm/brotli/commits | grep "sha" | head -n 1 | cut -d '"' -f 4)
# 使用提取的 commit hash 通过 go get 获取 tfo-go
go get github.com/andybalholm/brotli@$brotli_commit_hash

# 获取 xray 最新的 commits
xray_commit_hash=$(curl -s https://api.github.com/repos/XTLS/Xray-core/commits | grep "sha" | head -n 1 | cut -d '"' -f 4)
# 使用提取的 commit hash 通过 go get 获取 xray
go get github.com/xtls/xray-core@$xray_commit_hash

# 获取 gorm 最新的 commits
gorm_commit_hash=$(curl -s https://api.github.com/repos/go-gorm/gorm/commits | grep "sha" | head -n 1 | cut -d '"' -f 4)
# 使用提取的 commit hash 通过 go get 获取 gorm
go get gorm.io/gorm@$gorm_commit_hash

# 获取 utls 最新的 commits
utls_commit_hash=$(curl -s https://api.github.com/repos/refraction-networking/utls/commits | grep "sha" | head -n 1 | cut -d '"' -f 4)
# 使用提取的 commit hash 通过 go get 获取 gorm
go get github.com/refraction-networking/utls@$utls_commit_hash

# 获取 uuid 最新的 commits
uuid_commit_hash=$(curl -s https://api.github.com/repos/google/uuid/commits | grep "sha" | head -n 1 | cut -d '"' -f 4)
# 使用提取的 commit hash 通过 go get 获取 gorm
go get github.com/google/uuid@$uuid_commit_hash

# 获取 goconvey 最新的 commits
goconvey_commit_hash=$(curl -s https://api.github.com/repos/smartystreets/goconvey/commits | grep "sha" | head -n 1 | cut -d '"' -f 4)
# 使用提取的 commit hash 通过 go get 获取 gorm
go get github.com/smartystreets/goconvey@$goconvey_commit_hash

# 获取 gopherjs 最新的 commits
gopherjs_commit_hash=$(curl -s https://api.github.com/repos/gopherjs/gopherjs/commits | grep "sha" | head -n 1 | cut -d '"' -f 4)
# 使用提取的 commit hash 通过 go get 获取 gorm
go get github.com/gopherjs/gopherjs@$gopherjs_commit_hash

# 获取 uuid 最新的 commits
uuid_commit_hash=$(curl -s https://api.github.com/repos/google/uuid/commits | grep "sha" | head -n 1 | cut -d '"' -f 4)
# 使用提取的 commit hash 通过 go get 获取 gorm
go get github.com/google/uuid@$uuid_commit_hash

# 获取 socks5 最新的 commits
socks5_commit_hash=$(curl -s https://api.github.com/repos/Potterli20/socks5-fork/commits | grep "sha" | head -n 1 | cut -d '"' -f 4)
# 使用提取的 commit hash 通过 go get 获取 gorm
go get github.com/Potterli20/socks5-fork@$socks5_commit_hash
