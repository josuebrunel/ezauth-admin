# Builds the example app (examples/chi): the admin panel mounted in a small chi
# app, on SQLite. Build from the repository root:
#
#   docker build -f examples/chi/Dockerfile -t ezauth-admin-example .

FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# The SQLite driver is pure Go, so the binary is static and needs no libc.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/example ./examples/chi \
 && mkdir -p /out/app/data

FROM gcr.io/distroless/static-debian12:nonroot
# /app/data is where the SQLite file lives. It is created here, owned by the
# non-root user, so a named volume mounted on it is writable.
COPY --from=build --chown=nonroot:nonroot /out/app /app
COPY --from=build /out/example /usr/local/bin/example
WORKDIR /app
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/example"]
