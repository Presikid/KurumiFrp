FROM golang:1.23-bookworm AS build
WORKDIR /src
COPY go.mod VERSION README.md ./
COPY cmd ./cmd
COPY internal ./internal
COPY web ./web
COPY scripts/bundled_frp.py ./scripts/bundled_frp.py
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /out/kurumifrp ./cmd/kurumi && \
    python3 -c "import sys; sys.path.insert(0, 'scripts'); import bundled_frp; bundled_frp.prepare('linux_${TARGETARCH}', '/out/bundled')"

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*
WORKDIR /opt/kurumifrp
COPY --from=build /out/kurumifrp /opt/kurumifrp/kurumifrp
COPY --from=build /out/bundled /opt/kurumifrp/bundled
VOLUME ["/opt/kurumifrp/data"]
EXPOSE 9000 7000
ENTRYPOINT ["/opt/kurumifrp/kurumifrp", "-listen", "0.0.0.0:9000", "-data", "/opt/kurumifrp/data"]
