# 获取 tfo-go 最新的 commits
tfo_commit_hash=$(curl -s https://api.github.com/repos/database64128/tfo-go/commits | grep "sha" | head -n 1 | cut -d '"' -f 4)
# 使用提取的 commit hash 通过 go get 获取 tfo-go
go get github.com/database64128/tfo-go/v2@$tfo_commit_hash

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

# 获取 go-genproto 最新的 commits
go-genproto_commit_hash=$(curl -s https://api.github.com/repos/googleapis/go-genproto/commits | grep "sha" | head -n 1 | cut -d '"' -f 4)
# 使用提取的 commit hash 通过 go get 获取 gorm
go get google.golang.org/genproto@$go-genproto_commit_hash