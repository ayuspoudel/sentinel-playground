FROM golang:1.24-alpine AS builder

WORKDIR /app

COPY go.mod ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o load-generator

FROM alpine:3.20

WORKDIR /app

COPY --from=builder /app/load-generator .

CMD ["sh", "-c", "sleep 5 && ./load-generator"]
