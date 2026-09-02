FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/hyoketsu-server .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /out/hyoketsu-server /usr/local/bin/hyoketsu-server
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/hyoketsu-server"]
