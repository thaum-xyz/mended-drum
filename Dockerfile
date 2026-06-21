FROM --platform=$BUILDPLATFORM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -ldflags="-s -w" -o /out/mended-drum ./cmd/mended-drum

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/mended-drum /usr/local/bin/mended-drum
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/mended-drum"]
