FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY *.go openapi.json ./
RUN go build -o server .

FROM alpine:3.19
RUN apk add --no-cache ca-certificates wget
RUN adduser -D -u 1001 chirp
WORKDIR /app
COPY --from=builder /app/server ./server
RUN chown chirp:chirp /app/server
USER chirp
EXPOSE 8080
CMD ["./server"]
