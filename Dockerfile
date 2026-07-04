FROM golang:1.23-alpine AS build

WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/homelab-access ./cmd/homelab-access

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/homelab-access /homelab-access
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/homelab-access"]
