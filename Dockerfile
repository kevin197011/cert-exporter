# 构建阶段
FROM golang:1.24-alpine AS builder

WORKDIR /app

# 复制go mod文件并下载依赖（利用Docker层缓存）
COPY go.mod go.sum ./
RUN go mod download && go mod verify

# 复制源代码
COPY *.go ./

# 构建应用（优化构建参数）
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags='-w -s -extldflags "-static"' \
    -a -installsuffix cgo \
    -o cert-exporter .

# 运行阶段
FROM alpine:latest

RUN apk --no-cache add ca-certificates tzdata

# 创建应用用户
RUN addgroup -g 1001 -S appgroup && \
    adduser -u 1001 -S appuser -G appgroup

WORKDIR /app

# 从构建阶段复制二进制文件
COPY --from=builder /app/cert-exporter .
RUN chown appuser:appgroup cert-exporter

# 切换到非root用户
USER appuser

# 暴露端口
EXPOSE 8080

# 运行应用
CMD ["./cert-exporter"]