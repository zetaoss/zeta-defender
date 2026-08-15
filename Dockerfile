FROM golang:1.26.5-alpine AS build

WORKDIR /src
RUN apk add --no-cache ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w -X main.version=${VERSION} -X main.defaultConfigPath=/zeta-defender/etc/config.yaml" \
    -o /out/zeta-defender \
    ./cmd/zeta-defender
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w -X main.version=${VERSION} -X main.defaultConfigPath=/zeta-defender/etc/config.yaml" \
    -o /out/defendertool \
    ./cmd/defendertool

FROM alpine:3.24.1

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/zeta-defender /zeta-defender/bin/zeta-defender
COPY --from=build /out/defendertool /zeta-defender/bin/defendertool

USER 65532:65532
EXPOSE 8080
STOPSIGNAL SIGTERM

CMD ["/zeta-defender/bin/zeta-defender"]
