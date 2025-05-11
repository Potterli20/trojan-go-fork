# Go 官方支持的 GOOS/GOARCH 组合（参考自 https://gist.github.com/asukakenji/f15ba7e588ac42795f421b48b8aede63 ）
# 各平台支持的架构如下：
# aix:         ppc64
# android:     386, amd64, arm, arm64, riscv64
# darwin:      amd64, arm64
# dragonfly:   amd64
# freebsd:     386, amd64, arm, arm64, riscv64
# illumos:     amd64
# ios:         arm64
# js:          wasm
# linux:       386, amd64, arm, arm64, loong64, mips, mipsle, mips64, mips64le, ppc64, ppc64le, riscv64, s390x
# netbsd:      386, amd64, arm, arm64
# openbsd:     386, amd64, arm, arm64, mips64, riscv64
# plan9:       386, amd64, arm
# solaris:     amd64
# windows:     386, amd64, arm, arm64
#
# 最低支持平台和架构要求（参考 https://go.dev/wiki/MinimumRequirements ）：
# - 详见官方文档，不同 GOOS/GOARCH 组合对内核、glibc、Windows 版本等有最低要求。
# - 例如：linux/amd64 需要内核 >= 2.6.23，glibc >= 2.17；windows/amd64 需要 Windows 7/Server 2008R2 或更高。
# - 某些架构如 loong64 仅支持 Go 1.19 及以上版本。
# loong64 架构说明（Go 1.19+）：
# Go 编译器始终生成可在 LA364、LA464、LA664 或更高版本处理器上运行的 loong64 二进制文件。
#   LA364: 支持非对齐内存访问，128位SIMD，典型处理器如 loongson-2K2000/2K3000 等。
#   LA464: 支持非对齐内存访问，128/256位SIMD，典型处理器如 loongson-3A5000/3C5000/3D5000 等。
#   LA664: 支持非对齐内存访问，128/256位SIMD，典型处理器如 loongson-3A6000/3C6000 等。

NAME := trojan-go-fork
PACKAGE_NAME := github.com/Potterli20/trojan-go-fork
VERSION := `git describe --always`
COMMIT := `git rev-parse HEAD`

PLATFORM := linux
BUILD_DIR := build
VAR_SETTING := -X $(PACKAGE_NAME)/constant.Version=$(VERSION) -X $(PACKAGE_NAME)/constant.Commit=$(COMMIT)
GOBUILD = env CGO_ENABLED=0 go build -tags "full" -trimpath -ldflags="-s -w -buildid= $(VAR_SETTING)" -o $(BUILD_DIR)

.PHONY: trojan-go-fork release test
normal: clean trojan-go-fork

clean:
	rm -rf $(BUILD_DIR) *.zip *.dat

geoip.dat:
	wget https://github.com/v2fly/geoip/raw/release/geoip.dat -O geoip.dat

geoip-only-cn-private.dat:
	wget https://github.com/v2fly/geoip/raw/release/geoip-only-cn-private.dat -O geoip-only-cn-private.dat

geosite.dat:
	wget https://github.com/v2fly/domain-list-community/raw/release/dlc.dat -O geosite.dat

test:
	# Disable Bloomfilter when testing
	SHADOWSOCKS_SF_CAPACITY="-1" $(GO_DIR)go test -v ./...

trojan-go-fork:
	mkdir -p $(BUILD_DIR)
	$(GOBUILD)

install: $(BUILD_DIR)/$(NAME) geoip.dat geoip-only-cn-private.dat geosite.dat
	mkdir -p /etc/$(NAME)
	mkdir -p /usr/share/$(NAME)
	cp example/*.json /etc/$(NAME)
	cp $(BUILD_DIR)/$(NAME) /usr/bin/$(NAME)
	cp example/$(NAME).service /usr/lib/systemd/system/
	cp example/$(NAME)@.service /usr/lib/systemd/system/
	systemctl daemon-reload
	cp geosite.dat /usr/share/$(NAME)/geosite.dat
	cp geoip.dat /usr/share/$(NAME)/geoip.dat
	cp geoip-only-cn-private.dat /usr/share/$(NAME)/geoip-only-cn-private.dat
	ln -fs /usr/share/$(NAME)/geoip.dat /usr/bin/
	ln -fs /usr/share/$(NAME)/geoip-only-cn-private.dat /usr/bin/
	ln -fs /usr/share/$(NAME)/geosite.dat /usr/bin/

uninstall:
	rm /usr/lib/systemd/system/$(NAME).service
	rm /usr/lib/systemd/system/$(NAME)@.service
	systemctl daemon-reload
	rm /usr/bin/$(NAME)
	rm -rd /etc/$(NAME)
	rm -rd /usr/share/$(NAME)
	rm /usr/bin/geoip.dat
	rm /usr/bin/geoip-only-cn-private.dat
	rm /usr/bin/geosite.dat

%.zip: % geosite.dat geoip.dat geoip-only-cn-private.dat
	@if [ -d "$(BUILD_DIR)/$<" ] && [ "$(shell ls -A $(BUILD_DIR)/$<)" ]; then \
		zip -du $(NAME)-$@ -j $(BUILD_DIR)/$</*; \
	else \
		echo "Warning: $(BUILD_DIR)/$< is empty or does not exist"; \
	fi
	@zip -du $(NAME)-$@ example/*
	@-zip -du $(NAME)-$@ *.dat
	@echo "<<< ---- $(NAME)-$@"

# 通用构建规则，支持 GOMIPS、GO386 和 GOARM 参数
define BUILD_RULE
$(1):
	mkdir -p $(BUILD_DIR)/$(1)
	GOARCH=$(2) GOOS=$(3) \
	$(if $(4),GOAMD64=$(4),) \
	$(if $(filter mips%,$(2)),GOMIPS=$(5),) \
	$(if $(filter 386,$(2)),GO386=$(6),) \
	$(if $(filter arm,$(2)),GOARM=$(7),) \
	$(GOBUILD) -o $(BUILD_DIR)/$(1)/$(NAME)
endef

# 定义支持的平台和架构（仅包含官方支持的组合）
PLATFORMS := darwin linux freebsd netbsd openbsd windows
ARCHS_darwin   := amd64 arm64
ARCHS_linux    := 386 amd64 arm arm64 loong64 mips mipsle mips64 mips64le ppc64 ppc64le riscv64 s390x
ARCHS_freebsd  := 386 amd64 arm arm64 riscv64
ARCHS_netbsd   := 386 amd64 arm arm64
ARCHS_openbsd  := 386 amd64 arm arm64 mips64 riscv64
ARCHS_windows  := 386 amd64 arm arm64

GOAMD64_VARIANTS := v2 v3 v4
LOONG64_VARIANTS := LA364 LA464 LA664

# 动态生成所有目标
$(foreach platform,$(PLATFORMS), \
  $(foreach arch,$(ARCHS_$(platform)), \
    $(if $(findstring amd64,$(arch)), \
      $(foreach variant,$(GOAMD64_VARIANTS), \
        $(eval $(call BUILD_RULE,$(platform)-$(arch)-$(variant),$(arch),$(platform),$(variant))) \
      ) \
    , \
    $(if $(findstring loong64,$(arch)), \
      $(foreach variant,$(LOONG64_VARIANTS), \
        $(eval $(call BUILD_RULE,$(platform)-$(arch)-$(variant),$(arch),$(platform),$(variant))) \
      ) \
    , \
    $(if $(findstring mips,$(arch)), \
      $(foreach mips_abi,softfloat hardfloat, \
        $(eval $(call BUILD_RULE,$(platform)-$(arch)-$(mips_abi),$(arch),$(platform),,$(mips_abi))) \
      ) \
    , \
    $(if $(findstring 386,$(arch)), \
      $(foreach x86_abi,softfloat sse2, \
        $(eval $(call BUILD_RULE,$(platform)-$(arch)-$(x86_abi),$(arch),$(platform),,,$(x86_abi))) \
      ) \
    , \
    $(if $(filter arm,$(arch)), \
      $(foreach arm_version,v6 v7, \
        $(eval $(call BUILD_RULE,$(platform)-$(arch)-v$(arm_version),$(arch),$(platform),,,,$(arm_version))) \
      ) \
    , \
      $(eval $(call BUILD_RULE,$(platform)-$(arch),$(arch),$(platform))) \
    ))))) \
  ) \
)

# 生成所有 zip 包目标名
ALL_ZIPS := \
$(foreach platform,$(PLATFORMS), \
  $(foreach arch,$(ARCHS_$(platform)), \
    $(if $(findstring amd64,$(arch)), \
      $(foreach variant,$(GOAMD64_VARIANTS),$(platform)-$(arch)-$(variant).zip), \
    $(if $(findstring loong64,$(arch)), \
      $(foreach variant,$(LOONG64_VARIANTS),$(platform)-$(arch)-$(variant).zip), \
      $(if $(findstring mips,$(arch)), \
        $(foreach float_type,softfloat hardfloat,$(platform)-$(arch)-$(float_type).zip), \
        $(if $(filter arm,$(arch)), \
          $(foreach arm_version,v6 v7,$(platform)-$(arch)-v$(arm_version).zip), \
          $(if $(findstring 386,$(arch)), \
            $(foreach float_type,softfloat sse2,$(platform)-$(arch)-$(float_type).zip), \
            $(platform)-$(arch).zip \
          ) \
        ) \
      ) \
    ) \
    ) \
  ) \
)

# 更新 release 目标
release: geosite.dat geoip.dat geoip-only-cn-private.dat $(ALL_ZIPS)
