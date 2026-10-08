# syntax=docker/dockerfile:1

FROM golang:1.22-alpine AS build
WORKDIR /src

# 先复制依赖清单，让业务源码变化时仍可复用模块下载缓存。
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/migrate ./cmd/migrate

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S app && adduser -S -G app app
WORKDIR /app
COPY --from=build /out/api /out/migrate /app/
USER app
EXPOSE 8080
CMD ["/app/api"]
