FROM golang:1.17.5-alpine3.15 AS builder
WORKDIR /trojan-go-fork
RUN apk add git make gcc g++ libtool
COPY . .
RUN make trojan-go-fork &&\
    wget https://github.com/v2fly/domain-list-community/raw/release/dlc.dat -O build/geosite.dat &&\
    wget https://github.com/v2fly/geoip/raw/release/geoip.dat -O build/geoip.dat

FROM alpine
WORKDIR /
RUN apk add --no-cache tzdata ca-certificates
COPY --from=builder /trojan-go-fork/build /usr/local/bin/
COPY --from=builder /trojan-go-fork/example/server.json /etc/trojan-go-fork/config.json

ENTRYPOINT ["/usr/local/bin/trojan-go-fork", "-config"]
CMD ["/etc/trojan-go-fork/config.json"]
