FROM golang:alpine AS builder
WORKDIR /trojan-go-fork
ARG REF
RUN apk update && \
    apk add --no-cache git make wget build-base && \
    git clone https://github.com/Potterli20/trojan-go-fork.git . && \
    if [ -n "${REF}" ]; then \
        echo "Use commit ${REF}" && \
        git checkout ${REF}; \
    else \
        echo "No specific commit provided, use the latest one."; \
    fi && \
    make && \
    sh scripts/fetch-geo.sh build

FROM alpine
WORKDIR /
RUN apk add --no-cache tzdata ca-certificates
COPY --from=builder /trojan-go-fork/build /usr/local/bin/
COPY --from=builder /trojan-go-fork/example/server.json /etc/trojan-go-fork/config.json

ENTRYPOINT ["/usr/local/bin/trojan-go-fork", "-config"]
CMD ["/etc/trojan-go-fork/config.json"]
