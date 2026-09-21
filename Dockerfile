FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /moderation-service ./cmd/moderation-service

FROM alpine:3.21
RUN adduser -D -H -u 10001 appuser
WORKDIR /app
COPY --from=build /moderation-service /usr/local/bin/moderation-service
COPY config /app/config
RUN mkdir /app/data && chown -R appuser:appuser /app
USER appuser
EXPOSE 8080
ENTRYPOINT ["moderation-service"]
