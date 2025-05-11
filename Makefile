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

# 定义支持的平台和架构
PLATFORMS := darwin linux freebsd netbsd openbsd windows
ARCHS := amd64 arm64 arm 386 riscv64 ppc64 ppc64le s390x mips mipsle mips64 mips64le loong64
GOAMD64_VARIANTS := v2 v3 v4

# 动态生成所有目标
$(foreach platform,$(PLATFORMS), \
  $(foreach arch,$(ARCHS), \
    $(if $(or $(and $(filter arm,$(arch)),$(filter darwin,$(platform))), \
              $(and $(filter 386,$(arch)),$(filter darwin,$(platform)))),, \
      $(if $(findstring amd64,$(arch)), \
        $(foreach variant,$(GOAMD64_VARIANTS), \
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
      )))) \
    ) \
  ) \
)

# 生成所有 zip 包目标名
ALL_ZIPS := \
$(foreach platform,$(PLATFORMS), \
  $(foreach arch,$(ARCHS), \
    $(if $(or $(and $(filter arm,$(arch)),$(filter darwin,$(platform))), \
              $(and $(filter 386,$(arch)),$(filter darwin,$(platform)))),, \
      $(if $(findstring amd64,$(arch)), \
        $(foreach variant,$(GOAMD64_VARIANTS),$(platform)-$(arch)-$(variant).zip), \
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