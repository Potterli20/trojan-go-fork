FROM golang:alpine AS builder

WORKDIR /app
COPY . /app

ARG TARGETPLATFORM
ARG TARGETOS
ARG TARGETARCH
ARG VERSION
ARG CGO_ENABLED=on
ARG REF

ENV GOOS=$TARGETOS \
    GOARCH=$TARGETARCH

RUN apk update &&\
    apk add --no-cache git make wget build-base &&\
    git clone https://gitlab.atcatw.org/atca/community-edition/trojan-go.git
RUN if [[ -z "${REF}" ]]; then \
    echo "No specific commit provided, use the latest one." \
    ;else \
    echo "Use commit ${REF}" &&\
    cd trojan-go &&\
    git checkout ${REF} \
    ;fi
RUN cd trojan-go &&\
    make &&\
    wget https://github.com/v2fly/domain-list-community/raw/release/dlc.dat -O build/geosite.dat &&\
    wget https://github.com/Loyalsoldier/geoip/raw/release/geoip.dat -O build/geoip.dat &&\
    wget https://github.com/Loyalsoldier/geoip/raw/release/geoip-only-cn-private.dat -O build/geoip-only-cn-private.dat

FROM alpine

ENV PATH=/usr/bin/trojan-go:$PATH \
    REMOTE=127.0.0.1:80 \
    LOCAL=0.0.0.0:443 \
    PASSWORD=

RUN apk add --no-cache tzdata ca-certificates
COPY --from=builder /app/trojan-go/build /usr/local/bin/
COPY --from=builder /app/trojan-go/example/server.json /etc/trojan-go/config.json

EXPOSE 443
EXPOSE 1234
EXPOSE 80

ENTRYPOINT ["/usr/local/bin/trojan-go", "-config"]
CMD ["/etc/trojan-go/config.json"]
