FROM golang:1.24-alpine AS builder

WORKDIR /app

# Копируем модули и загружаем зависимости
COPY go.mod go.sum ./
RUN go mod download
# Копируем весь исходный код и .en
COPY . .
# Собираем бинарник
RUN go build -o bot .

FROM alpine:latest

WORKDIR /app

# Копируем собранный бинарник из builder
COPY --from=builder /app/bot .

EXPOSE 7540

CMD ["./bot"]