FROM golang:1.26.4-bookworm@sha256:b305420a68d0f229d91eb3b3ed9e519fcf2cf5461da4bef997bf927e8c0bfd2b AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/nacos-secure-fixture ./cmd/nacos-secure-fixture
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/nacos-secure-fixture ./cmd/nacos-secure-fixture

FROM alpine:3.22

RUN apk add --no-cache ca-certificates wget \
    && adduser -D -H -u 10001 nekiro
COPY --from=build /out/nacos-secure-fixture /usr/local/bin/nacos-secure-fixture
USER nekiro
ENTRYPOINT ["/usr/local/bin/nacos-secure-fixture"]
