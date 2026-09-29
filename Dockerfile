# Node and Go are build tools only; the final image contains one executable.
ARG NODE_IMAGE=node:22-alpine
ARG GO_IMAGE=golang:1.27-alpine
FROM ${NODE_IMAGE} AS web
WORKDIR /src
COPY package.json package-lock.json ./
RUN npm ci
COPY frontend ./frontend
COPY vite.config.js ./
RUN npm run build:web

FROM ${GO_IMAGE} AS backend
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY assets.go ./
COPY cmd ./cmd
COPY internal ./internal
COPY examples/evaluated-scenarios ./examples/evaluated-scenarios
COPY --from=web /src/dist ./dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/agentmirror ./cmd/agentmirror && mkdir -p /out/data

FROM scratch
COPY --from=backend /out/agentmirror /agentmirror
COPY --from=backend --chown=65532:65532 /out/data /data
USER 65532:65532
WORKDIR /data
VOLUME ["/data"]
ENTRYPOINT ["/agentmirror"]
CMD ["--db", "/data/agentmirror-v2.sqlite3"]
