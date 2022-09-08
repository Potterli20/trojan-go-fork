NAME := trojan-go-fork
PACKAGE_NAME := github.com/Potterli20/trojan-go-fork
VERSION := `git describe --tags`
COMMIT := `git rev-parse HEAD`

PLATFORM := linux
BUILD_DIR := build
VAR_SETTING := -X $(PACKAGE_NAME)/constant.Version=$(VERSION) -X $(PACKAGE_NAME)/constant.Commit=$(COMMIT)

GOOS= ( linux darwin freebsd netbsd hardfloat softfloat openbsd )
ARCHS=( 386 amd64 arm arm64 ppc64le s390x riscv64 mips64 mips64le mipsle )
ARMS=( 5 6 7 )

for ARCH in ${ARCHS[@]}; do
    if [ "${ARCH}" = "arm" ]; then
        for V in ${ARMS[@]}; do
            echo "Building trojan-go-dev_linux_${ARCH}${V}"
            env CGO_ENABLED=0 GOOS=${GOOS} GOARCH=${ARCH} GOARM=${V} go build -v -tags "full" -ldflags "${LDFLAGS}" -o ${cur_dir}/trojan-go-dev_linux_${ARCH}${V}
        done
    else
        echo "Building trojan-go-dev_linux_${ARCH}"
        env CGO_ENABLED=0 GOOS=${GOOS} GOARCH=${ARCH} go build -v -tags "full" -ldflags "${LDFLAGS}" -o ${cur_dir}/trojan-go-dev_linux_${ARCH}
    fi
done

#GOBUILD = env CGO_ENABLED=0 $(GO_DIR)go build -tags "full" -trimpath -ldflags="-s -w -buildid= $(VAR_SETTING)" -o $(BUILD_DIR)

.PHONY: trojan-go-fork release test
normal: clean trojan-go-fork

clean:
	rm -rf $(BUILD_DIR)
	rm -f *.zip
	rm -f *.dat

geoip.dat:
	wget https://github.com/v2fly/geoip/raw/release/geoip.dat

geoip-only-cn-private.dat:
	wget https://github.com/v2fly/geoip/raw/release/geoip-only-cn-private.dat

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
	@zip -du $(NAME)-$@ -j $(BUILD_DIR)/$</*
	@zip -du $(NAME)-$@ example/*
	@-zip -du $(NAME)-$@ *.dat
	@echo "<<< ---- $(NAME)-$@"

release: geosite.dat geoip.dat geoip-only-cn-private.dat darwin-amd64.zip darwin-arm64.zip linux-386.zip linux-amd64.zip \
	linux-arm.zip linux-armv5.zip linux-armv6.zip linux-armv7.zip linux-armv8.zip  \
	linux-ppc64le.zip linux-s390x.zip linux-ppc64.zip linux-riscv64.zip linux-mips64.zip linux-mips64le.zip  \
	linux-mips-softfloat.zip linux-mips-hardfloat.zip linux-mipsle-softfloat.zip linux-mipsle-hardfloat.zip \
	freebsd-386.zip freebsd-amd64.zip freebsd-arm.zip freebsd-arm64.zip \
	netbsd-386.zip netbsd-amd64.zip netbsd-arm.zip netbsd-arm64.zip \
	openbsd-386.zip openbsd-amd64.zip openbsd-arm.zip openbsd-arm64.zip openbsd-mips64.zip \
	windows-386.zip windows-amd64.zip windows-arm.zip windows-armv6.zip windows-armv7.zip windows-arm64.zip 
	
