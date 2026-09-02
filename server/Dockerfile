FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/hyoketsu-server .

FROM alpine:3.20
# Ensure runtime utilities required for sqlite operations exist
RUN apk add --no-cache ca-certificates sqlite
RUN adduser -D -u 1000 hyoketsu

COPY --from=build /out/hyoketsu-server /usr/local/bin/hyoketsu-server

# Pre-create writable locations for non-root user (uid 1000)
RUN mkdir -p /data /tmp/hyoketsu && chown -R hyoketsu:hyoketsu /data /tmp/hyoketsu

USER hyoketsu
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/hyoketsu-server"]