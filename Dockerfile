FROM golang:1.22-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o /ixr ./cmd/ixr

FROM scratch
COPY --from=builder /ixr /ixr
EXPOSE 7000
ENTRYPOINT ["/ixr"]
