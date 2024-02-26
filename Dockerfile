FROM golang:latest

WORKDIR /trojan-go
COPY . /trojan-go

ARG TARGETPLATFORM
ARG TARGETOS
ARG TARGETARCH
ARG VERSION
ARG CGO_ENABLED=on
ARG REF

ENV GOOS=$TARGETOS \
    GOARCH=$TARGETARCH
RUN apt update
RUN apt install git make wget build-essential -y -f
RUN if [[ -z "${REF}" ]]; then \
        echo "No specific commit provided, use the latest one." \
    ;else \
        echo "Use commit ${REF}" &&\
        git checkout ${REF} \
    ;fi

RUN make
RUN wget https://github.com/v2fly/domain-list-community/raw/release/dlc.dat -O build/geosite.dat
RUN wget https://github.com/Loyalsoldier/geoip/raw/release/geoip.dat -O build/geoip.dat
RUN wget https://github.com/Loyalsoldier/geoip/raw/release/geoip-only-cn-private.dat -O build/geoip-only-cn-private.dat

ENV PATH=/usr/local/bin/trojan-go:$PATH \
    REMOTE=127.0.0.1:80 \
    LOCAL=0.0.0.0:443 \
    PASSWORD=

RUN apt install tzdata ca-certificates -y -f
RUN mv /trojan-go/build /usr/local/bin/trojan-go
RUN mkdir /etc/trojan-go
RUN mv /trojan-go/example/server.json /etc/trojan-go/config.json

EXPOSE 443
EXPOSE 80

ENTRYPOINT ["trojan-go", "-config"]
CMD ["/etc/trojan-go/config.json"]
