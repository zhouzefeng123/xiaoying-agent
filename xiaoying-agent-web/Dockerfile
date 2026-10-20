ARG NODE_IMAGE=node:22-bookworm-slim
ARG NGINX_IMAGE=nginx:1.27-alpine

FROM ${NODE_IMAGE} AS build

WORKDIR /src

ENV ELECTRON_SKIP_BINARY_DOWNLOAD=1 \
    npm_config_update_notifier=false

COPY package.json package-lock.json ./
RUN npm ci --no-audit --no-fund

COPY . .
RUN npm run build

FROM ${NGINX_IMAGE}

RUN sed -i 's|https://dl-cdn.alpinelinux.org|http://mirrors.aliyun.com|g' /etc/apk/repositories \
    && apk add --no-cache apache2-utils

COPY --from=build /src/dist /usr/share/nginx/html
COPY docker-entrypoint.d/40-htpasswd.sh /docker-entrypoint.d/40-htpasswd.sh

RUN sed -i 's/\r$//' /docker-entrypoint.d/40-htpasswd.sh \
    && chmod +x /docker-entrypoint.d/40-htpasswd.sh

EXPOSE 80
