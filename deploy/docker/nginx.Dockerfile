# Tunnex edge reverse proxy.
# Routes the SPA (/) to the web service and the API (/healthz, /api) to the API.

FROM nginx:1.27-alpine
COPY deploy/nginx/nginx.conf /etc/nginx/conf.d/default.conf
EXPOSE 80
