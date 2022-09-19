# syntax=docker/dockerfile:1

FROM golang:1.19-alpine as builder

RUN mkdir /opt/chatterbox
WORKDIR /opt/chatterbox

RUN apk add --no-cache git=2.36.2-r0 build-base=0.5-r3

COPY go.mod .
COPY go.sum .
RUN go mod download

COPY . .

WORKDIR /opt/chatterbox/cmd/server

RUN go build -o server

FROM alpine:3
COPY --from=builder /opt/chatterbox/sql /opt/chatterbox/sql
COPY --from=builder /opt/chatterbox/cmd/server/server /opt/chatterbox/server
EXPOSE 8443

WORKDIR /opt
CMD ["./server"]
