# syntax=docker/dockerfile:1.7
FROM golang:1.27.1-bookworm@sha256:69a7b9788769bec032d238959b61854e9ae87f57be9029ec04e9885fabf99195 AS go-base

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

FROM go-base AS app-build
COPY . .

ARG SERVICE
ARG TARGETOS=linux
ARG TARGETARCH
RUN test "${SERVICE}" = "api" || test "${SERVICE}" = "worker" || test "${SERVICE}" = "console" || test "${SERVICE}" = "chaoslab" || test "${SERVICE}" = "showcase"
RUN CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" \
    go build -trimpath -ldflags="-s -w" -o /out/service "./cmd/${SERVICE}"
RUN chmod 0555 /out/service

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab AS runtime
ARG SERVICE
ARG WDE_VERSION=dev
ARG WDE_REVISION=unknown
LABEL org.opencontainers.image.title="webhook-delivery-${SERVICE}" \
      org.opencontainers.image.version="${WDE_VERSION}" \
      org.opencontainers.image.revision="${WDE_REVISION}" \
      org.opencontainers.image.source="https://github.com/gabrieldosprazeres/go-webhook-delivery"
COPY --from=app-build --chown=nonroot:nonroot /out/service /service
USER nonroot:nonroot
ENTRYPOINT ["/service"]
