module github.com/Potterli20/trojan-go-fork

go 1.27.0

tool golang.org/x/tools/cmd/stringer

require (
	github.com/Potterli20/go-shadowsocks2 v0.0.0-20260811213656-71847589a521
	github.com/Potterli20/socks5-fork v0.0.0-20260901083004-9c2194c52f2c
	github.com/Potterli20/sqlite v0.0.0-20260826192821-81c43fa35221
	github.com/apernet/quic-go v0.61.1-0.20260806010916-184d081eef3e
	github.com/coder/websocket v1.8.15
	github.com/database64128/tfo-go/v2 v2.4.1-0.20260826113932-498aa0d59c9b
	github.com/go-sql-driver/mysql v1.10.0
	github.com/refraction-networking/utls v1.8.3-0.20260802151714-23b1dac19c06
	github.com/smartystreets/goconvey v1.8.2-0.20240306062457-a50310f1e3e5
	github.com/stretchr/testify v1.12.1
	github.com/xtaci/smux v1.5.58-0.20260515062718-ae956bb8d67b
	github.com/xtls/xray-core v1.260327.1-0.20260901045710-cd4ce973e9f6
	golang.org/x/crypto v0.55.0
	golang.org/x/net v0.58.0
	golang.org/x/sys v0.47.0
	golang.org/x/term v0.45.0
	golang.org/x/time v0.15.0
	google.golang.org/grpc v1.83.2
	google.golang.org/protobuf v1.36.12
	gopkg.in/yaml.v3 v3.0.1
	gorm.io/gorm v1.31.2
)

require (
	filippo.io/edwards25519 v1.2.0 // indirect
	github.com/andybalholm/brotli v1.2.3 // indirect
	github.com/database64128/netx-go v0.1.1 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/google/uuid v1.6.1-0.20241114170450-2d3c2a9cc518 // indirect
	github.com/gopherjs/gopherjs v1.21.1-0.20260727145006-490705b1d6fc // indirect
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/jinzhu/now v1.1.5 // indirect
	github.com/jtolds/gls v4.20.0+incompatible // indirect
	github.com/klauspost/compress v1.19.2 // indirect
	github.com/klauspost/cpuid/v2 v2.4.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/riobard/go-bloom v0.0.0-20200614022211-cdc8013cb5b3 // indirect
	github.com/smarty/assertions v1.15.1 // indirect
	github.com/txthinking/runnergroup v0.0.0-20250224021307-5864ffeb65ae // indirect
	github.com/zhigui-projects/gm-go v0.0.0-20200510034956-8e4ef670d055 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	go4.org/netipx v0.0.0-20231129151722-fdeea329fbba // indirect
	golang.org/x/exp v0.0.0-20260820142414-ca536658362e // indirect
	golang.org/x/mod v0.40.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	golang.org/x/tools v0.49.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260831171406-18b4a7587f8a // indirect
	modernc.org/libc v1.75.6 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
	modernc.org/sqlite v1.57.0 // indirect
)

replace (
	// quic-go 使用 HyNetworks fork 的 mod-rename 版本说明：
	// apernet/quic-go 在 v0.58+ 的正式 tag 中将 go.mod module 声明从
	// github.com/apernet/quic-go 改为了 github.com/quic-go/quic-go，导致直接
	// require 新 tag 会触发 "module declares its path as ... but was required as ..."
	// 错误。使用 HyNetworks/quic-go fork 上的 *-mod-rename 分支，这些分支的 module
	// 声明保留为 github.com/apernet/quic-go，代码与 apernet 上游对应正式版本一致。
	// 注意：mod-rename 是 branch 名不是 tag，不能直接写在 require/replace 里（CI
	// 清空 modcache 后会报 "is not a tag" 错误），必须使用该 branch HEAD commit
	// 生成的伪版本（vBASE-TIMESTAMP-HASH12），由 gomod.sh 中 update_quic_go 自动计算。
	github.com/apernet/quic-go v0.60.1-0.20260618182935-599b15a1fa26 => github.com/HyNetworks/quic-go v0.60.1-0.20260618182935-599b15a1fa26
	github.com/apernet/quic-go v0.61.1-0.20260806010916-184d081eef3e => github.com/HyNetworks/quic-go v0.61.1-0.20260806010916-184d081eef3e
)
