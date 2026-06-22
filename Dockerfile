# ── Stage 1: Build ──────────────────────────────────────────
FROM golang:1.26-alpine AS builder

WORKDIR /src
COPY go.* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/gokych ./cmd/gokych

# ── Stage 2: Runtime ─────────────────────────────────────────
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /out/gokych /app/gokych
COPY configs/ /app/configs/

WORKDIR /app

RUN mkdir -p /app/data/{settings,avatars,plugins,themes,typst}

EXPOSE 8000

ENTRYPOINT ["/app/gokych"]
