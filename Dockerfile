FROM golang:1.25-alpine AS builder
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/imcs ./cmd/imcs/

FROM alpine:3.20
RUN addgroup -S imcs && adduser -S -G imcs imcs \
    && mkdir -p /data && chown imcs:imcs /data
COPY --from=builder /out/imcs /usr/local/bin/imcs
USER imcs
WORKDIR /data
VOLUME /data
EXPOSE 6380
# Без пароля (IMCS_PASSWORD или -auth) protected mode пускает только loopback —
# снаружи контейнера подключиться нельзя. Задайте пароль:
#   docker run -e IMCS_PASSWORD=... imcs
STOPSIGNAL SIGTERM
ENTRYPOINT ["imcs", "-dir", "/data", "-port", ":6380"]
