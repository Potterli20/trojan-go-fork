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

RUN apk update
RUN apk add --no-cache --virtual .build-deps git make wget build-base libcap
RUN if [[ -z "${REF}" ]]; then \
        echo "No specific commit provided, use the latest one." \
    ;else \
        echo "Use commit ${REF}" && \
        git checkout ${REF} \
    ;fi
RUN make
RUN wget https://github.com/v2fly/domain-list-community/raw/release/dlc.dat -O build/geosite.dat
RUN wget https://github.com/Loyalsoldier/geoip/raw/release/geoip.dat -O build/geoip.dat
RUN wget https://github.com/Loyalsoldier/geoip/raw/release/geoip-only-cn-private.dat -O build/geoip-only-cn-private.dat

FROM alpine

ENV PATH=/usr/bin/trojan-go:$PATH \
    REMOTE=www.bing.com:80 \
    LOCAL=0.0.0.0:443 \
    PASSWORD=

WORKDIR /app
RUN apk add --no-cache tzdata ca-certificates
COPY --from=builder /app/build /usr/local/bin/
COPY --from=builder /app/example/server.json /etc/trojan-go/config.json

EXPOSE 443
EXPOSE 1234
EXPOSE 80

ENTRYPOINT ["trojan-go", "-config"]
CMD ["/etc/trojan-go/config.json"]
