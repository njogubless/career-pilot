# syntax=docker/dockerfile:1
FROM node:22-alpine AS build
WORKDIR /app
RUN corepack enable
COPY package.json pnpm-lock.yaml* pnpm-workspace.yaml* ./
RUN pnpm install --frozen-lockfile
COPY . .
RUN pnpm build

FROM nginxinc/nginx-unprivileged:1.31.7-alpine
# Apply Alpine security updates (the base image can lag them; CI Trivy fails
# on fixable HIGH and CRITICAL issues), then drop back to the nginx user.
USER root
RUN apk upgrade --no-cache
USER 101
ARG VERSION=dev
ARG REVISION=unknown
LABEL org.opencontainers.image.title="careerpilot-web" \
      org.opencontainers.image.description="CareerPilot AI web app (SPA and API proxy)" \
      org.opencontainers.image.version=$VERSION \
      org.opencontainers.image.revision=$REVISION
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /app/dist /usr/share/nginx/html
EXPOSE 8080
