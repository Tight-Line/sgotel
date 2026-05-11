FROM golang:1.22 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/sgotel ./cmd/sgotel

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/sgotel /sgotel
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/sgotel"]
