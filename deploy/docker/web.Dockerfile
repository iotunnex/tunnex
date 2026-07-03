# Tunnex web SPA — build with Node, serve static files with nginx.
# Build context is the repo root.

FROM node:20-alpine AS build
WORKDIR /app
RUN corepack enable

# Install workspace deps (web + shared) with good layer caching.
COPY package.json pnpm-workspace.yaml pnpm-lock.yaml* ./
COPY apps/web/package.json apps/web/package.json
COPY packages/shared/package.json packages/shared/package.json
RUN pnpm install --filter @tunnex/web... --no-frozen-lockfile

COPY packages/shared/ packages/shared/
COPY apps/web/ apps/web/
RUN pnpm --filter @tunnex/web build

FROM nginx:1.27-alpine
# SPA fallback so client-side routes resolve to index.html.
COPY deploy/nginx/spa.conf /etc/nginx/conf.d/default.conf
COPY --from=build /app/apps/web/dist /usr/share/nginx/html
EXPOSE 80
