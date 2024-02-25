NAME := trojan-go
PACKAGE_NAME := github.com/Potterli20/trojan-go
VERSION := `git describe --tags`
COMMIT := `git rev-parse HEAD`

PLATFORM := linux
BUILD_DIR := build
VAR_SETTING := -X $(PACKAGE_NAME)/constant.Version=$(VERSION) -X $(PACKAGE_NAME)/constant.Commit=$(COMMIT)
GOBUILD = env CGO_ENABLED=0 $(GO_DIR)go build -tags "full" -trimpath -ldflags="-s -w -buildid= $(VAR_SETTING)" -o $(BUILD_DIR)

.PHONY: trojan-go release test
normal: clean trojan-go

clean:
	rm -rf $(BUILD_DIR)
	rm -f *.zip
	rm -f *.dat

geoip.dat:
	wget https://github.com/v2fly/geoip/raw/release/geoip.dat -O geoip.dat

geoip-only-cn-private.dat:
	wget https://github.com/v2fly/geoip/raw/release/geoip-only-cn-private.dat -O geoip-only-cn-private.dat

geosite.dat:
	wget https://github.com/v2fly/domain-list-community/raw/release/dlc.dat -O geosite.dat

test:
	# Disable Bloomfilter when testing
	SHADOWSOCKS_SF_CAPACITY="-1" $(GO_DIR)go test -v ./...

trojan-go:
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

release: geosite.dat geoip.dat geoip-only-cn-private.dat \
  darwin-amd64.zip darwin-arm64.zip  linux-amd64.zip linux-loong64.zip \
	linux-ppc64le.zip linux-s390x.zip linux-ppc64.zip linux-riscv64.zip linux-mips64.zip linux-mips64le.zip  \
	linux-mips-softfloat.zip linux-mips-hardfloat.zip linux-mipsle-softfloat.zip linux-mipsle-hardfloat.zip \
	freebsd-amd64.zip freebsd-arm.zip freebsd-arm64.zip \
	netbsd-amd64.zip netbsd-arm.zip netbsd-arm64.zip \
	openbsd-amd64.zip openbsd-arm.zip openbsd-arm64.zip openbsd-ppc64.zip \
	windows-amd64.zip windows-arm.zip windows-armv6.zip windows-armv7.zip windows-arm64.zip \
  darwin-amd64-v2.zip linux-amd64-v2.zip freebsd-amd64-v2.zip netbsd-amd64-v2.zip openbsd-amd64-v2.zip windows-amd64-v2.zip \
  darwin-amd64-v3.zip linux-amd64-v3.zip freebsd-amd64-v3.zip netbsd-amd64-v3.zip openbsd-amd64-v3.zip windows-amd64-v3.zip \
  darwin-amd64-v4.zip linux-amd64-v4.zip freebsd-amd64-v4.zip netbsd-amd64-v4.zip openbsd-amd64-v4.zip windows-amd64-v4.zip \
  linux-386-sse2.zip freebsd-386-sse2.zip darwin-386-sse2.zip windows-386-sse2.zip netbsd-386-sse2.zip openbsd-386-sse2.zip \
  linux-386-softfloat.zip freebsd-386-softfloat.zip darwin-386-softfloat.zip windows-386-softfloat.zip netbsd-386-softfloat.zip openbsd-386-softfloat.zip

#amd64(v2、v3、v4)
darwin-amd64-v2:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=amd64 GOAMD64=v2 GOOS=darwin $(GOBUILD)/$@

linux-amd64-v2:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=amd64 GOAMD64=v2 GOOS=linux $(GOBUILD)/$@

freebsd-amd64-v2:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=amd64 GOAMD64=v2 GOOS=freebsd $(GOBUILD)/$@

netbsd-amd64-v2:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=amd64 GOAMD64=v2 GOOS=netbsd $(GOBUILD)/$@

openbsd-amd64-v2:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=amd64 GOAMD64=v2 GOOS=openbsd $(GOBUILD)/$@

windows-amd64-v2:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=amd64 GOAMD64=v2 GOOS=windows $(GOBUILD)/$@

darwin-amd64-v3:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=amd64 GOAMD64=v3 GOOS=darwin $(GOBUILD)/$@

linux-amd64-v3:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=amd64 GOAMD64=v3 GOOS=linux $(GOBUILD)/$@

freebsd-amd64-v3:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=amd64 GOAMD64=v3 GOOS=freebsd $(GOBUILD)/$@

netbsd-amd64-v3:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=amd64 GOAMD64=v3 GOOS=netbsd $(GOBUILD)/$@

openbsd-amd64-v3:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=amd64 GOAMD64=v3 GOOS=openbsd $(GOBUILD)/$@

windows-amd64-v3:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=amd64 GOAMD64=v3 GOOS=windows $(GOBUILD)/$@

darwin-amd64-v4:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=amd64 GOAMD64=v4 GOOS=darwin $(GOBUILD)/$@

linux-amd64-v4:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=amd64 GOAMD64=v4 GOOS=linux $(GOBUILD)/$@

freebsd-amd64-v4:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=amd64 GOAMD64=v4 GOOS=freebsd $(GOBUILD)/$@

netbsd-amd64-v4:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=amd64 GOAMD64=v4 GOOS=netbsd $(GOBUILD)/$@

openbsd-amd64-v4:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=amd64 GOAMD64=v4 GOOS=openbsd $(GOBUILD)/$@

windows-amd64-v4:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=amd64 GOAMD64=v4 GOOS=windows $(GOBUILD)/$@

#386(sse2、softfloat)
darwin-386-sse2:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=amd64 GOOS=darwin GO386=sse2 $(GOBUILD)/$@

linux-386-sse2:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=386 GOOS=linux GO386=sse2 $(GOBUILD)/$@

freebsd-386-sse2:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=386 GOOS=freebsd GO386=sse2 $(GOBUILD)/$@

netbsd-386-sse2:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=386 GOOS=netbsd GO386=sse2 $(GOBUILD)/$@

openbsd-386-sse2:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=386 GOOS=openbsd GO386=sse2 $(GOBUILD)/$@

windows-386-sse2:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=386 GOOS=windows GO386=sse2 $(GOBUILD)/$@

darwin-386-softfloat:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=amd64 GOOS=darwin GO386=softfloat $(GOBUILD)/$@

linux-386-softfloat:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=386 GOOS=linux GO386=softfloat $(GOBUILD)/$@

freebsd-386-softfloat:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=386 GOOS=freebsd GO386=softfloat $(GOBUILD)/$@

netbsd-386-softfloat:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=386 GOOS=netbsd GO386=softfloat $(GOBUILD)/$@

openbsd-386-softfloat:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=386 GOOS=openbsd GO386=softfloat $(GOBUILD)/$@

windows-386-softfloat:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=386 GOOS=windows GO386=softfloat $(GOBUILD)/$@

#other

darwin-amd64:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=amd64 GOOS=darwin $(GOBUILD)/$@

darwin-arm64:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=arm64 GOOS=darwin $(GOBUILD)/$@

linux-ppc64:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=ppc64 GOOS=linux $(GOBUILD)/$@

linux-riscv64:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=riscv64 GOOS=linux $(GOBUILD)/$@

linux-amd64:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=amd64 GOOS=linux $(GOBUILD)/$@

linux-mips-softfloat:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=mips GOMIPS=softfloat GOOS=linux $(GOBUILD)/$@

linux-mips-hardfloat:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=mips GOMIPS=hardfloat GOOS=linux $(GOBUILD)/$@

linux-mipsle-softfloat:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=mipsle GOMIPS=softfloat GOOS=linux $(GOBUILD)/$@

linux-mipsle-hardfloat:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=mipsle GOMIPS=hardfloat GOOS=linux $(GOBUILD)/$@

linux-mips64:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=mips64 GOOS=linux $(GOBUILD)/$@

linux-mips64le:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=mips64le GOOS=linux $(GOBUILD)/$@

linux-ppc64le:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=mips64le GOOS=linux $(GOBUILD)/$@

linux-s390x:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=s390x GOOS=linux $(GOBUILD)/$@

linux-loong64:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=loong64 GOOS=linux $(GOBUILD)/$@

freebsd-amd64:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=amd64 GOOS=freebsd $(GOBUILD)/$@

freebsd-arm:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=arm GOOS=freebsd $(GOBUILD)/$@

freebsd-arm64:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=arm64 GOOS=freebsd $(GOBUILD)/$@

freebsd-riscv64:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=riscv64 GOOS=freebsd $(GOBUILD)/$@

netbsd-amd64:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=amd64 GOOS=netbsd $(GOBUILD)/$@

netbsd-arm:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=arm GOOS=netbsd $(GOBUILD)/$@

netbsd-arm64:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=arm64 GOOS=netbsd $(GOBUILD)/$@

openbsd-amd64:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=amd64 GOOS=openbsd $(GOBUILD)/$@

openbsd-arm:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=arm GOOS=openbsd $(GOBUILD)/$@

openbsd-arm64:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=arm64 GOOS=openbsd $(GOBUILD)/$@

openbsd-ppc64:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=ppc64 GOOS=openbsd $(GOBUILD)/$@

windows-amd64:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=amd64 GOOS=windows $(GOBUILD)/$@

windows-arm:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=arm GOOS=windows $(GOBUILD)/$@

windows-armv6:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=arm GOOS=windows GOARM=6 $(GOBUILD)/$@

windows-armv7:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=arm GOOS=windows GOARM=7 $(GOBUILD)/$@

windows-arm64:
	mkdir -p $(BUILD_DIR)/$@
	GOARCH=arm64 GOOS=windows $(GOBUILD)/$@
