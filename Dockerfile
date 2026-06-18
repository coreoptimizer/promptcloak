# syntax=docker/dockerfile:1

FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/extproc ./cmd/extproc

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/extproc /usr/local/bin/extproc
# 9002: gRPC ext_proc, 8080: HTTP health
EXPOSE 9002 8080
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/extproc"]
