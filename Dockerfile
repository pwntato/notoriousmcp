FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY . .
RUN go mod download && go build -o bin/server ./cmd/local

FROM alpine:3.21
RUN apk --no-cache add ca-certificates
WORKDIR /app
COPY --from=builder /app/bin/server .
EXPOSE 3000
CMD ["./server"]
