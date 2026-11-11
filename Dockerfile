FROM golang:1.27.3-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /bin/finfe-api ./cmd/api

FROM alpine:3.22
RUN addgroup -S finfe && adduser -S -G finfe finfe
COPY --from=build /bin/finfe-api /usr/local/bin/finfe-api
USER finfe
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/finfe-api"]
