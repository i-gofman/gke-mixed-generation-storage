FROM golang:1.27-alpine AS build

ARG VERSION=dev
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X main.version=${VERSION}" \
      -o /out/mixed-fleet-check ./cmd/mixed-fleet-check

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/mixed-fleet-check /usr/local/bin/mixed-fleet-check
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/mixed-fleet-check"]
