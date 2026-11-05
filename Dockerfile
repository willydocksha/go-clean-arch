# build stage
FROM golang:1.27-alpine AS builder
WORKDIR /src

# cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# build static binaries (api, migrate, seed)
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/migrate ./cmd/migrate \
 && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/seed ./cmd/seed

# runtime stage
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/ /
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/api"]
