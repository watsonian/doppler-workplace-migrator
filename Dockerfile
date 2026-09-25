# Stage 1: Build frontend
FROM node:24.14-alpine AS frontend-builder
ARG PNPM_VERSION=11.22.0
WORKDIR /app/frontend
RUN npm install -g pnpm@$PNPM_VERSION
COPY frontend/package.json frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile
COPY frontend/ ./
RUN pnpm run build

# Stage 2: Build backend
FROM golang:1.25-alpine AS backend-builder
WORKDIR /app
COPY backend/ ./backend/
COPY --from=frontend-builder /app/frontend/dist ./backend/webui
WORKDIR /app/backend
RUN CGO_ENABLED=0 GOOS=linux go build -o /migrator ./cmd/server

# Stage 3: Runtime
FROM gcr.io/distroless/static-debian12
COPY --from=backend-builder /migrator /migrator
# Run unprivileged. distroless ships a nonroot user (uid 65532); the checkpoint
# volume must be writable by it.
USER nonroot:nonroot
ENV HOST=0.0.0.0
ENV CHECKPOINT_DIR=/data
EXPOSE 8080
ENTRYPOINT ["/migrator"]
