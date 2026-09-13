FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /kakeratta .

# The page is not in the image. Firebase Hosting serves frontend/dist and
# rewrites /api/** here, so this is the API and nothing else.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /kakeratta /kakeratta
EXPOSE 8787
ENTRYPOINT ["/kakeratta"]
