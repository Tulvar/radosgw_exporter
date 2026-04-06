FROM golang:1.25-bookworm AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o radosgw_exporter .

FROM debian:bookworm-slim

RUN apt-get update && \
    apt-get install -y --no-install-recommends ca-certificates tzdata && \
    rm -rf /var/lib/apt/lists/*

RUN useradd -u 10001 -r -s /sbin/nologin -d /nonexistent nonroot

COPY --from=builder /app/radosgw_exporter /radosgw_exporter

USER 10001

EXPOSE 9242

ENTRYPOINT ["/radosgw_exporter"]