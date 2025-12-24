FROM golang:1.24-alpine AS build
WORKDIR /app
COPY go.mod .
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o workload

FROM gcr.io/distroless/base-debian12
WORKDIR /
COPY --from=build /app/workload /workload
EXPOSE 8080
ENTRYPOINT ["/workload"]
