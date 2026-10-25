# KubeScope collector + UI in one small image.
# Build: docker build -t kubescope:local .

# 1. UI (vite writes into collector/internal/server/dist)
FROM node:24-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN mkdir -p ../collector/internal/server/dist && npm run build

# 2. Collector, with the UI embedded (pure Go SQLite, so a static binary)
FROM golang:1.27-alpine AS collector
WORKDIR /src/collector
COPY collector/go.mod collector/go.sum ./
RUN go mod download
COPY collector/ ./
COPY --from=web /src/collector/internal/server/dist ./internal/server/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /kubescope .

# 3. Runtime: no shell, no package manager, non-root by default
FROM gcr.io/distroless/static-debian12:nonroot
# Lets cleanup find every KubeScope build, including old ones that lost their name.
LABEL io.kubescope.image=collector
COPY --from=collector /kubescope /kubescope
EXPOSE 8090
VOLUME /data
ENTRYPOINT ["/kubescope", "-addr", ":8090", "-db", "/data/kubescope.db"]
