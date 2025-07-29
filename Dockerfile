FROM golang:1.24 AS builder

WORKDIR /metrics

# get dependencies
COPY go.mod go.sum ./
RUN go mod download

# copy source
COPY . .
# codegen
RUN go generate ./...
# build
RUN CGO_ENABLED=1 go build -o bin/ -tags='netgo timetzdata' -trimpath -a -ldflags '-s -w -linkmode external -extldflags "-static"'  ./cmd/metricd

FROM debian:bookworm-slim

LABEL maintainer="The Sia Foundation <info@sia.tech>" \
org.opencontainers.image.description.vendor="The Sia Foundation" \
org.opencontainers.image.description="A metricd container - provides metrics from the Sia blockchain" \
org.opencontainers.image.source="https://github.com/SiaFoundation/metrics" \
org.opencontainers.image.licenses=MIT

# copy binary and certificates
COPY --from=builder /metrics/bin/* /usr/bin/
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/


VOLUME [ "/data" ]
# API port
EXPOSE 9980/tcp
# RPC port
EXPOSE 9981/tcp

ENTRYPOINT [ "metricd", "--dir", "/data" ]
