ARG PLATFORM=linux/amd64

FROM --platform=$PLATFORM golang:1.26-alpine3.23 AS builder

RUN apk add --no-cache git

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o flixflox ./cmd/server

FROM --platform=$PLATFORM alpine:3.23

RUN apk add --no-cache ffmpeg ca-certificates tzdata

WORKDIR /app

COPY --from=builder /build/flixflox .

RUN mkdir -p /app/uploads

EXPOSE 7777

CMD ["/app/flixflox"]