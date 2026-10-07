# FirePanel 单镜像：内嵌前端 + 纯 Go SQLite（无 CGO）
FROM golang:1.22-alpine AS build
RUN apk add --no-cache nodejs npm bash
WORKDIR /src
COPY frontend/ frontend/
RUN cd frontend && npm ci --no-audit --no-fund && npm run build
COPY backend/ backend/
RUN cd backend && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags "-s -w" -o /out/firepanel ./cmd/firepanel

FROM alpine:3.20
# iptables-nft / nftables 二选一按探测自动使用；ip6tables 同步
RUN apk add --no-cache ca-certificates nftables iptables ip6tables tzdata && \
    addgroup -S fp && adduser -S fp -G fp || true
COPY --from=build /out/firepanel /usr/local/bin/firepanel
# 需要操作宿主内核 netfilter：--cap-add=NET_ADMIN（compose 已带）
VOLUME /data
EXPOSE 18088 80 443
ENTRYPOINT ["/usr/local/bin/firepanel"]
CMD ["-listen", ":18088", "-data-dir", "/data"]
