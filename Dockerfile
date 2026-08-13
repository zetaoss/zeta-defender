FROM golang:1.26.5-alpine AS build

WORKDIR /src
RUN apk add --no-cache ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-s -w" \
    -o /out/zeta-defender \
    ./cmd/zeta-defender

FROM scratch

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/zeta-defender /zeta-defender

USER 65532:65532
EXPOSE 8080
STOPSIGNAL SIGTERM

ENTRYPOINT ["/zeta-defender"]
CMD ["-config", "/etc/zeta-defender/config.yaml"]
