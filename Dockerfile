FROM golang:1.23-alpine AS builder

RUN apk add --no-cache git

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o flixflox ./cmd/server

FROM alpine:3.20

RUN apk add --no-cache ffmpeg ca-certificates tzdata

WORKDIR /app

COPY --from=builder /build/flixflox .

RUN mkdir -p /app/uploads

EXPOSE 5000

CMD ["./flixflox"]
