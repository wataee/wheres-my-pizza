# 1. Stage сборки (Builder)
FROM golang:1.24-alpine AS builder

WORKDIR /app

# Сначала копируем файлы зависимостей (для кэширования слоев Docker)
COPY go.mod go.sum ./
RUN go mod download

# Копируем исходный код
COPY . .

# Собираем бинарник
# CGO_ENABLED=0 делает бинарник статическим (работает везде)
RUN CGO_ENABLED=0 GOOS=linux go build -o restaurant-system ./cmd/main.go

# 2. Stage запуска (Runner)
FROM alpine:latest

WORKDIR /root/

# Копируем бинарник из первого этапа
COPY --from=builder /app/restaurant-system .
# Копируем конфиг (он нужен для запуска)
COPY --from=builder /app/config.yaml .

# Entrypoint позволяет передавать аргументы (флаги) при запуске
ENTRYPOINT ["./restaurant-system"]