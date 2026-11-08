# Build: docker build -t homey-exporter .
# Run:   docker run -p 9414:9414 -e HOMEY_URL=http://192.168.1.10 -e HOMEY_TOKEN=... homey-exporter
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG COMMIT=none
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" -o /homey-exporter .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /homey-exporter /homey-exporter
EXPOSE 9414
USER nonroot:nonroot
ENTRYPOINT ["/homey-exporter"]
CMD ["-config", "/etc/homey-exporter/config.json"]
