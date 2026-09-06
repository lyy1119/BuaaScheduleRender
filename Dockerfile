# 构建阶段：使用 golang:1.27 镜像编译
FROM golang:1.27 AS build
WORKDIR /src

# 先复制依赖清单（便于利用构建缓存）
COPY go.mod ./
COPY go.sum ./ 2>/dev/null || true

# 复制全部源码并编译 cmd/server
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

# 运行阶段：alpine 镜像，设置上海时区
FROM alpine:3.20
# 安装 ca-certificates（HTTPS 需要）与 tzdata（时区数据），并把 /etc/localtime 指向上海
RUN apk add --no-cache ca-certificates tzdata \
    && cp /usr/share/zoneinfo/Asia/Shanghai /etc/localtime \
    && echo "Asia/Shanghai" > /etc/timezone \
    && rm -rf /var/cache/apk/*
ENV TZ=Asia/Shanghai

WORKDIR /app
COPY --from=build /out/server /app/server

# 端口与默认启动参数：-addr :8080；docker run 附加参数会追加到 ENTRYPOINT 之后
# （例如追加 -debug / -landscape / -first "2026-09-07" 等）
EXPOSE 8080
ENTRYPOINT ["/app/server"]
CMD ["-addr", ":8080"]
