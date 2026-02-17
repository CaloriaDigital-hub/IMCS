FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o imcs ./cmd/imcs/

FROM alpine:3.19
RUN apk add --no-cache ca-certificates
WORKDIR /data
COPY --from=builder /app/imcs /usr/local/bin/imcs
EXPOSE 6380
VOLUME /data
ENTRYPOINT ["imcs", "-dir", "/data", "-port", ":6380"]