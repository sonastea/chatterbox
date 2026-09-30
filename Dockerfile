# syntax=docker/dockerfile:1

FROM golang:1.27-alpine AS builder

RUN mkdir /opt/chatterbox
WORKDIR /opt/chatterbox

RUN apk add --no-cache git

COPY go.mod .
COPY go.sum .
RUN go mod download

COPY . .

WORKDIR /opt/chatterbox/cmd/server

RUN CGO_ENABLED=0 go build -o server

FROM alpine:3
COPY --from=builder /opt/chatterbox/cmd/server/server /opt/chatterbox/server
RUN apk add --no-cache ca-certificates && mkdir -p /opt/chatterbox/certs /opt/chatterbox/data
ENV DATABASE_URL=file:/opt/chatterbox/data/chatterbox.db
EXPOSE 8443

WORKDIR /opt/chatterbox
CMD ["./server"]
