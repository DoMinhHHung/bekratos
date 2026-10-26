FROM golang:1.27-alpine AS builder
RUN apk add --no-cache ca-certificates git
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/control-plane ./cmd/control-plane

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S bekratos \
    && adduser -S -G bekratos -H -s /sbin/nologin bekratos
WORKDIR /app
COPY --from=builder /out/control-plane /app/control-plane
USER bekratos
EXPOSE 8080
ENTRYPOINT ["/app/control-plane"]
