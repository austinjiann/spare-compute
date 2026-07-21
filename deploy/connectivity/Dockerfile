FROM golang:1.26-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG RAILWAY_GIT_COMMIT_SHA=dev
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w -X main.version=${RAILWAY_GIT_COMMIT_SHA}" -o /out/computehop-connectivity ./cmd/computehop-connectivity

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/computehop-connectivity /usr/local/bin/computehop-connectivity
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/computehop-connectivity"]
