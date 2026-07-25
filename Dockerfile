FROM golang:1.23-alpine AS build

WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/homelab-access ./cmd/homelab-access

FROM gcr.io/distroless/static-debian12:nonroot

ARG OCI_CREATED
ARG OCI_REVISION
ARG OCI_SOURCE=https://github.com/petebeegle/homelab-access

LABEL org.opencontainers.image.created="${OCI_CREATED}" \
      org.opencontainers.image.revision="${OCI_REVISION}" \
      org.opencontainers.image.source="${OCI_SOURCE}"

COPY --from=build /out/homelab-access /homelab-access
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/homelab-access"]
