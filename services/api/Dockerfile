# syntax=docker/dockerfile:1
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/careerpilot ./cmd/careerpilot

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/careerpilot /careerpilot
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/careerpilot"]
CMD ["serve"]
