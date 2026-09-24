# syntax=docker/dockerfile:1.7
FROM golang:1.27.1-bookworm AS go-base

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

FROM go-base AS app-build
COPY . .

ARG SERVICE
ARG TARGETOS=linux
ARG TARGETARCH
RUN test "${SERVICE}" = "api" || test "${SERVICE}" = "worker" || test "${SERVICE}" = "chaoslab"
RUN CGO_ENABLED=0 GOOS="${TARGETOS}" GOARCH="${TARGETARCH}" \
    go build -trimpath -ldflags="-s -w" -o /out/service "./cmd/${SERVICE}"

FROM gcr.io/distroless/static-debian12:nonroot AS runtime
COPY --from=app-build /out/service /service
USER nonroot:nonroot
ENTRYPOINT ["/service"]
