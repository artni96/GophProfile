FROM golang:1.27-alpine AS builder

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download && go mod verify

COPY . .

RUN GOOS=linux go build -o /cmd/server /build/cmd/server

ENTRYPOINT ["/cmd/server"]