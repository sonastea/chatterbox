# syntax=docker/dockerfile:1

FROM golang:1.27-alpine

RUN mkdir /opt/chatterbox
WORKDIR /opt/chatterbox

RUN apk add --no-cache git build-base

COPY go.mod .
COPY go.sum .
RUN go mod download

COPY . .

RUN go build ./cmd/server

EXPOSE 8443

CMD ["./server"]
