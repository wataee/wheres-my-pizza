FROM library/golang:1.23-alpine AS builder
WORKDIR /app
COPY . .
RUN go build -o restaurant-system .

FROM alpine:latest
WORKDIR /root/
COPY --from=builder /app/restaurant-system .
COPY --from=builder /app/config.yaml .
ENTRYPOINT ["./restaurant-system"]
