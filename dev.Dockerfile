# syntax=docker/dockerfile:1

FROM golang:1.22-alpine

RUN mkdir /opt/chatterbox
WORKDIR /opt/chatterbox

RUN apk add --no-cache git=2.43.0-r0 build-base=0.5-r3

COPY go.mod .
COPY go.sum .
RUN go mod download

COPY . .

RUN go build ./cmd/server

EXPOSE 8443

CMD ["./server"]
