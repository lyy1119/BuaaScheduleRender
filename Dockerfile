# 构建阶段：使用 Go 官方镜像编译（CGO 关闭 → 完全静态，可运行于 scratch）
FROM golang:1.27 AS build

# tzdata 提供 /usr/share/zoneinfo（运行阶段拷入 scratch 以支持 Asia/Shanghai）；
# ca-certificates 提供根证书（HTTPS 访问教务系统用）。
RUN apt-get update \
    && apt-get install -y --no-install-recommends tzdata ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src
# 依赖清单先复制，源码改动不会让 go 重新下载依赖
COPY go.mod ./
COPY . .

# VERSION 由 CI 注入（-ldflags -X main.version=…），会出现在 /help 中
ARG VERSION=docker
RUN CGO_ENABLED=0 GOOS=linux go build \
        -trimpath \
        -ldflags "-s -w -X main.version=${VERSION}" \
        -o /out/server ./cmd/server

# 校验产物确实是静态链接：scratch 里没有动态链接器，动态二进制启动即失败
RUN if grep -qa 'ld-linux\|ld-musl\|/lib/ld-' /out/server; then \
        echo "ERROR: 二进制非静态链接，scratch 无法运行" >&2; \
        exit 1; \
    fi

# 运行阶段：scratch（无 shell、无包管理器、无 libc）
FROM scratch

# 上海时区与根证书
COPY --from=build /usr/share/zoneinfo/Asia/Shanghai /etc/localtime
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
ENV TZ=Asia/Shanghai

COPY --from=build /out/server /server

EXPOSE 8080
# ENTRYPOINT 固定二进制，默认参数 -addr :8080；
# 附加参数（-debug / -landscape / -first …）会追加在其后。
ENTRYPOINT ["/server"]
CMD ["-addr", ":8080"]
