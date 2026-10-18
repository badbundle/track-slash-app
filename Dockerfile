FROM golang:1.26-bookworm AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/trackd ./cmd/trackd \
	&& mkdir -p /out/storage

FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app

ENV PORT=8080 \
	TRACK_SLASH_STORAGE_BACKEND=local \
	TRACK_SLASH_STORAGE_LOCAL_ROOT=/var/lib/track-slash/storage \
	TRACK_SLASH_STORAGE_BUCKET=local

COPY --from=build /out/trackd /app/trackd
COPY --from=build --chown=65532:65532 /out/storage /var/lib/track-slash/storage

USER 65532:65532
VOLUME ["/var/lib/track-slash/storage"]
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=30s --retries=3 \
	CMD ["/app/trackd", "-healthcheck"]

ENTRYPOINT ["/app/trackd"]
