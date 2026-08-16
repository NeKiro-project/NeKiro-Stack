FROM golang:1.26.4-bookworm@sha256:b305420a68d0f229d91eb3b3ed9e519fcf2cf5461da4bef997bf927e8c0bfd2b AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY tests ./tests
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/evaluation-config ./cmd/evaluation-config \
    && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/nacos-secure-fixture ./cmd/nacos-secure-fixture \
    && CGO_ENABLED=0 GOOS=linux go test -c -tags=e2e -o /out/backend.test ./tests/backend

FROM docker:28.3.3-dind
RUN apk add --no-cache ca-certificates docker-cli-compose jq
WORKDIR /opt/nekiro
COPY --from=build /out/evaluation-config /out/nacos-secure-fixture /out/backend.test ./
COPY compose.yaml compose.router-nacos-secure.yaml compose.evaluation.yaml ./
COPY scripts/evaluate.sh ./evaluate.sh

ARG NEKIRO_CONTROL_PLANE_IMAGE
ARG NEKIRO_A2A_ROUTER_IMAGE
ARG NEKIRO_CONSOLE_IMAGE
ARG NEKIRO_RUNTIME_A_IMAGE
ARG NEKIRO_RUNTIME_B_IMAGE
ARG NEKIRO_NACOS_SECURE_PROXY_IMAGE
ENV NEKIRO_CONTROL_PLANE_IMAGE=$NEKIRO_CONTROL_PLANE_IMAGE \
    NEKIRO_A2A_ROUTER_IMAGE=$NEKIRO_A2A_ROUTER_IMAGE \
    NEKIRO_CONSOLE_IMAGE=$NEKIRO_CONSOLE_IMAGE \
    NEKIRO_RUNTIME_A_IMAGE=$NEKIRO_RUNTIME_A_IMAGE \
    NEKIRO_RUNTIME_B_IMAGE=$NEKIRO_RUNTIME_B_IMAGE \
    NEKIRO_NACOS_SECURE_PROXY_IMAGE=$NEKIRO_NACOS_SECURE_PROXY_IMAGE

ENTRYPOINT ["/opt/nekiro/evaluate.sh"]
