# syntax=docker/dockerfile:1.7
FROM golang:1.24.7-alpine@sha256:fc2cff6625f3c1c92e6c85938ac5bd09034ad0d4bc2dfb08278020b68540dbb5 AS build
ARG TARGETOS=linux
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -mod=readonly -trimpath -buildvcs=false \
    -ldflags="-s -w -buildid= -X github.com/thomas-illiet/jupyter-kubeopencode/internal/version.Version=${VERSION} -X github.com/thomas-illiet/jupyter-kubeopencode/internal/version.Commit=${COMMIT} -X github.com/thomas-illiet/jupyter-kubeopencode/internal/version.Date=${BUILD_DATE}" \
    -o /out/mediator ./cmd/mediator

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
LABEL org.opencontainers.image.title="jupyter-kubeopencode-mediator" \
      org.opencontainers.image.description="Secure JupyterHub mediator for KubeOpenCode" \
      org.opencontainers.image.source="https://github.com/thomas-illiet/jupyter-kubeopencode" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.created="${BUILD_DATE}" \
      org.opencontainers.image.licenses="Apache-2.0"
COPY --from=build /out/mediator /mediator
USER 65532:65532
ENTRYPOINT ["/mediator"]

