# ── Stage 1: Build ──────────────────────────────────────────
FROM golang:1.26-alpine AS builder

WORKDIR /src
COPY go.* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/gokych ./cmd/gokych

# ── Stage 2: Runtime ─────────────────────────────────────────
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata wget

# Create non-root user for security (following principle of least privilege)
RUN addgroup --system --gid 1001 gokych \
 && adduser --system --uid 1001 --ingroup gokych gokych

COPY --from=builder /out/gokych /app/gokych
COPY configs/ /app/configs/

WORKDIR /app

# Create data directory structure with proper ownership
RUN mkdir -p /app/data/{settings,avatars,plugins,themes,typst,uploads} \
 && chown -R gokych:gokych /app/data /app/configs

# Make binary executable
RUN chmod +x /app/gokych

USER gokych

EXPOSE 8000

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=5 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:8000/api/health || exit 1

ENTRYPOINT ["/app/gokych"]
