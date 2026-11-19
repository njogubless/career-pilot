# syntax=docker/dockerfile:1
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/careerpilot ./cmd/careerpilot

FROM gcr.io/distroless/static-debian12:nonroot
ARG VERSION=dev
ARG REVISION=unknown
# APP_VERSION defaults to the build version; an explicit value overrides it.
ENV APP_VERSION=$VERSION
LABEL org.opencontainers.image.title="careerpilot-api" \
      org.opencontainers.image.description="CareerPilot AI core platform (API and worker)" \
      org.opencontainers.image.version=$VERSION \
      org.opencontainers.image.revision=$REVISION
COPY --from=build /out/careerpilot /careerpilot
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/careerpilot"]
CMD ["serve"]
