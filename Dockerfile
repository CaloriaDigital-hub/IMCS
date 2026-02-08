FROM golang:1.25-alpine AS builder
WORKDIR /app

COPY go.mod ./
RUN go mod download

COPY . .

RUN go build -o imcs-server main.go


FROM alpine:latest

WORKDIR /root/

RUN mkdir -p cache-files

COPY --from=builder /app/imcs-server .

EXPOSE 8080

CMD ["./imcs-server"]