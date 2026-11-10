FROM golang:1.26-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
COPY third_party/frp ./third_party/frp
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
COPY web ./web
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/kurumi ./cmd/kurumi \
    && mkdir -p /out/data \
    && chmod 0700 /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build --chown=65532:65532 /out/kurumi /kurumi
COPY --from=build --chown=65532:65532 /out/data /data
COPY --from=build /src/internal/embedded/LICENSE-frp /licenses/LICENSE-frp
USER 65532:65532
VOLUME ["/data"]
ENTRYPOINT ["/kurumi"]
CMD ["--data", "/data", "--listen", "127.0.0.1:9000"]
