FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags "-s -w" -o /relay ./cmd/relay

FROM scratch
COPY --from=build /relay /relay
EXPOSE 8080
ENTRYPOINT ["/relay"]
