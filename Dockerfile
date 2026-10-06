# Versioned upstream image; pin its digest in your release registry after validation.
ARG HYSTERIA_IMAGE=tobyxdd/hysteria:v2.12.2
FROM ${HYSTERIA_IMAGE} AS hysteria
FROM golang:1.26.8-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/nimbus ./cmd/nimbus
FROM alpine:3.23 AS control
RUN apk add --no-cache ca-certificates
COPY --from=build /out/nimbus /usr/local/bin/nimbus
USER 10001:10001
ENTRYPOINT ["/usr/local/bin/nimbus"]
CMD ["control"]
FROM control AS gateway
COPY --from=hysteria /usr/local/bin/hysteria /usr/local/bin/hysteria
CMD ["agent"]
