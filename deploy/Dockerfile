# FirePanel 单镜像：内嵌前端 + 纯 Go SQLite（无 CGO）
FROM golang:1.26-alpine AS build
RUN apk add --no-cache nodejs npm bash
WORKDIR /src
COPY frontend/ frontend/
RUN cd frontend && npm ci --no-audit --no-fund && npm run build
COPY backend/ backend/
# 同步前端产物到 embed 目录（本地由 build.sh 完成，镜像内必须显式执行）
RUN mkdir -p backend/internal/web/dist && cp -r frontend/dist/. backend/internal/web/dist/
ARG VERSION=dev
RUN cd backend && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/firepanel ./cmd/firepanel

FROM alpine:3.20
# iptables-nft / nftables 二选一按探测自动使用；ip6tables 同步。
# iproute2 提供 ss：端口台账与连接级流量依赖它（缺失时这两块功能不可用）；
# wget 供 healthcheck 使用（busybox 自带）。
RUN apk add --no-cache ca-certificates nftables iptables ip6tables iproute2 tzdata
COPY --from=build /out/firepanel /usr/local/bin/firepanel
# 容器以宿主机 root 运行：创建/插入宿主机 iptables/nft 链必须 root + NET_ADMIN。
# 不要再加降权用户（面板要写 /etc/caddy、执行 sysctl 等）。
VOLUME /data
EXPOSE 18088 80 443
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -q -O - http://127.0.0.1:18088/api/v1/bootstrap >/dev/null || exit 1
ENTRYPOINT ["/usr/local/bin/firepanel"]
CMD ["-listen", ":18088", "-data-dir", "/data"]
