FROM --platform=$BUILDPLATFORM golang:1.22-alpine AS builder
ARG TARGETOS=linux
ARG TARGETARCH=arm64
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -o /ixr ./cmd/ixr

FROM scratch
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /ixr /ixr
EXPOSE 8080
ENTRYPOINT ["/ixr"]
