# ── Stage 1: deps ─────────────────────────────────────────────
# Separate deps from sources so Docker can cache the layer.
FROM node:20-alpine AS deps
WORKDIR /app

COPY package.json package-lock.json* ./
RUN npm ci --no-audit --no-fund

# ── Stage 2: build ────────────────────────────────────────────
FROM node:20-alpine AS builder
WORKDIR /app

COPY --from=deps /app/node_modules ./node_modules
COPY . .

# Standalone output drops node_modules from the runtime image (~95% smaller)
# and bundles only the dependencies Next.js actually uses. Requires the
# matching `output: "standalone"` setting in next.config.ts.
#
# STANDALONE=1 opts into `output: "standalone"` in next.config.ts AND bypasses
# the production guard that otherwise requires NEXT_PUBLIC_API_BASE_URL (the
# docker-compose path talks to the backend over the docker network via
# API_BASE_URL=backend:8000, so the public env is intentionally unset).
ARG STANDALONE=1
ENV STANDALONE=${STANDALONE}
ENV NEXT_TELEMETRY_DISABLED=1
RUN npm run build

# ── Stage 3: runtime ──────────────────────────────────────────
FROM node:20-alpine AS runner
WORKDIR /app

ENV NODE_ENV=production
ENV NEXT_TELEMETRY_DISABLED=1
ENV PORT=3000
ENV HOSTNAME=0.0.0.0

# Non-root runtime — Next.js standalone ships as `server.js` and runs
# under the bundled `node` binary inside node_modules.
RUN addgroup --system --gid 1001 nodejs \
 && adduser  --system --uid 1001 --ingroup nodejs nextjs

# Install wget for HEALTHCHECK (alpine base doesn't include curl/wget).
# --no-cache keeps the image small.
RUN apk add --no-cache wget

COPY --from=builder /app/public ./public
COPY --from=builder --chown=nextjs:nodejs /app/.next/standalone ./
COPY --from=builder --chown=nextjs:nodejs /app/.next/static ./.next/static

USER nextjs

EXPOSE 3000

HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:3000/ || exit 1

CMD ["node", "server.js"]