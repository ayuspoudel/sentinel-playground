FROM golang:1.24-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o workload

FROM alpine:3.20

WORKDIR /app

COPY --from=builder /app/workload .

EXPOSE 8080

CMD ["./workload"]
