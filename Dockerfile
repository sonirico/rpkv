FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags '-s -w' -o /rpkv ./cmd/rpkv

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /rpkv /rpkv
EXPOSE 8080
ENTRYPOINT ["/rpkv"]
