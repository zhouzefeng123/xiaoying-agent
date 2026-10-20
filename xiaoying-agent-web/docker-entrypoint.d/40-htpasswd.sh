#!/bin/sh
set -eu

: "${BASIC_AUTH_USER:?BASIC_AUTH_USER is required}"
: "${BASIC_AUTH_PASSWORD:?BASIC_AUTH_PASSWORD is required}"

mkdir -p /etc/nginx/auth
htpasswd -bc /etc/nginx/auth/.htpasswd "$BASIC_AUTH_USER" "$BASIC_AUTH_PASSWORD"
chown nginx:nginx /etc/nginx/auth/.htpasswd
chmod 640 /etc/nginx/auth/.htpasswd
