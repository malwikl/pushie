FROM golang:1.27.1-alpine AS builder
WORKDIR /src
COPY src/go.mod src/go.sum ./
RUN go mod download
COPY src/ ./
RUN build_version=$(go run ./cmd/buildversion) && CGO_ENABLED=0 go build -mod=readonly -trimpath -ldflags="-s -w -X main.buildVersion=$build_version" -o /pushie .
FROM alpine:3.23
RUN apk add --no-cache ca-certificates && addgroup -S app && adduser -S -G app app && mkdir -p /data && chown -R app:app /data
COPY --from=builder /pushie /usr/local/bin/pushie
USER app
EXPOSE 8099
ENV ADDR=:8099 DB_PATH=/data/webpush.sqlite
CMD ["/usr/local/bin/pushie"]
