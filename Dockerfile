FROM node:22-bookworm-slim AS frontend
WORKDIR /src/frontend
COPY frontend/ ./
RUN node build.mjs

FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /src/frontend/dist ./frontend/dist
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /kakeratta .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /kakeratta /kakeratta
EXPOSE 8787
ENTRYPOINT ["/kakeratta"]
