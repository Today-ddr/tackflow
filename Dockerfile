# syntax=docker/dockerfile:1.7

FROM node:24-alpine AS web
WORKDIR /src
RUN corepack enable && corepack prepare pnpm@12.18.0 --activate
COPY web/package.json web/pnpm-lock.yaml ./web/
WORKDIR /src/web
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm build

FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_TIME=unknown
RUN CGO_ENABLED=0 go build -trimpath \
	-ldflags "-X main.version=${VERSION} -X main.commit=${COMMIT} -X main.buildTime=${BUILD_TIME}" \
	-o /out/tack ./cmd/tack && \
	CGO_ENABLED=0 go build -trimpath -o /out/updater ./cmd/updater

FROM gcr.io/distroless/static-debian12:nonroot AS app
COPY --from=build /out/tack /tack
EXPOSE 8080
HEALTHCHECK --interval=15s --timeout=3s --retries=3 CMD ["/tack", "healthcheck"]
ENTRYPOINT ["/tack"]

FROM docker:27.5.1-cli AS updater
RUN apk add --no-cache docker-cli-compose
COPY --from=build /out/updater /updater
ENTRYPOINT ["/updater"]
