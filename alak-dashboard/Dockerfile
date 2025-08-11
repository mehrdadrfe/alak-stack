# ---- builder ----
FROM node:20-bookworm AS builder
WORKDIR /app

COPY package*.json ./
RUN npm ci --no-audit --no-fund

COPY . .
ENV NODE_ENV=production
RUN npm run build

# ---- runner ----
FROM node:20-bookworm-slim AS runner
WORKDIR /app
ENV NODE_ENV=production
ENV PORT=3000
ENV HOSTNAME=0.0.0.0

# Optional: drop privileges
RUN useradd -m -u 10001 nextjs
USER nextjs

# Copy the minimal standalone server and assets
COPY --chown=nextjs:nextjs --from=builder /app/.next/standalone ./
COPY --chown=nextjs:nextjs --from=builder /app/.next/static ./.next/static
COPY --chown=nextjs:nextjs --from=builder /app/public ./public

EXPOSE 3000
CMD ["node", "server.js"]
