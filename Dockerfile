FROM golang:alpine AS builder
WORKDIR /
ARG REF
RUN apk update &&\
    apk add --no-cache git make wget build-base &&\
    git clone https://github.com/Potterli20/trojan-go-fork.git
RUN if [[ -z "${REF}" ]]; then \
        echo "No specific commit provided, use the latest one." \
    ;else \
        echo "Use commit ${REF}" &&\
        cd trojan-go-fork &&\
        git checkout ${REF} \
    ;fi
RUN cd trojan-go-fork &&\
    make &&\
    wget https://github.com/v2fly/domain-list-community/raw/release/dlc.dat -O build/geosite.dat &&\
    wget https://github.com/v2fly/geoip/raw/release/geoip.dat -O build/geoip.dat &&\
    wget https://github.com/v2fly/geoip/raw/release/geoip-only-cn-private.dat -O build/geoip-only-cn-private.dat

FROM alpine
WORKDIR /
RUN apk add --no-cache tzdata ca-certificates
COPY --from=builder /trojan-go-fork/build /usr/local/bin/
COPY --from=builder /trojan-go-fork/example/server.json /etc/trojan-go-fork/config.json

ENTRYPOINT ["/usr/local/bin/trojan-go-fork", "-config"]
CMD ["/etc/trojan-go-fork/config.json"]
