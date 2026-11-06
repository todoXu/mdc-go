# syntax=docker/dockerfile:1
# MDC Go 镜像。程序是纯 Go（CGO_ENABLED=0），前端、默认配置、内置插件都已编译进程序。
#
# 构建：docker build -t mdc-go .
# 多平台：docker buildx build --platform linux/amd64,linux/arm64 -t <用户名>/mdc-go:0.1 --push .

# ---- 编译 ----
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
WORKDIR /src
# 先只复制依赖清单，依赖没变时可以复用这一层缓存。
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
# buildx 会按 --platform 传入 TARGETOS/TARGETARCH，在编译机上交叉编译出目标平台的程序。
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags '-s -w' -o /out/mdc-go ./cmd/mdc-go

# ---- 运行 ----
FROM alpine:3.22
# ca-certificates：访问 HTTPS 数据源；tzdata：日志和「今天」按 TZ 时区计算；
# ffmpeg：提供 ffprobe，用于识别视频真实分辨率（不需要时可删掉，镜像能小约 100 MB）。
RUN apk add --no-cache ca-certificates tzdata ffmpeg
ENV TZ=Asia/Shanghai
COPY --from=build /out/mdc-go /app/mdc-go
# data：配置、刮削数据库、日志、演员映射表；plugins：数据源插件。
# 两个目录为空时，程序第一次启动会按内置默认生成内容。
VOLUME ["/app/data", "/app/plugins"]
EXPOSE 19208
ENTRYPOINT ["/app/mdc-go", "-data", "/app/data", "-plugins", "/app/plugins", "-listen", "0.0.0.0:19208"]
