ARG GO_IMAGE=golang:1.25-bookworm
ARG RUNTIME_IMAGE=debian:bookworm-slim

FROM ${GO_IMAGE} AS build

WORKDIR /src

ENV CGO_ENABLED=0 \
    GOPROXY=https://goproxy.cn,direct \
    GOTOOLCHAIN=local

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGETARCH
RUN set -eux; \
    arch="${TARGETARCH:-amd64}"; \
    CGO_ENABLED=0 GOOS=linux GOARCH="${arch}" \
    go build -trimpath -ldflags="-s -w" -o /out/auramate .

FROM ${RUNTIME_IMAGE}

ENV TZ=Asia/Shanghai \
    DEBIAN_FRONTEND=noninteractive

RUN if [ -f /etc/apt/sources.list.d/debian.sources ]; then \
        sed -i 's|http://deb.debian.org|http://mirrors.aliyun.com|g' /etc/apt/sources.list.d/debian.sources; \
    fi; \
    if [ -f /etc/apt/sources.list ]; then \
        sed -i 's|http://deb.debian.org|http://mirrors.aliyun.com|g' /etc/apt/sources.list; \
    fi; \
    apt-get update \
    && apt-get install -y --no-install-recommends \
        bash \
        ca-certificates \
        curl \
        git \
        python3 \
        python3-pip \
        ripgrep \
        tzdata \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY --from=build /out/auramate /app/auramate
COPY system_skills /app/system_skills
COPY tools /app/tools

RUN mkdir -p /app/workspace /app/database /app/logs

EXPOSE 3000

ENTRYPOINT ["/app/auramate"]
CMD ["server", "--port", "3000"]
