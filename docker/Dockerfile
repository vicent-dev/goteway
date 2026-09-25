FROM golang:1.27

RUN mkdir /app

ADD . /app

WORKDIR /app

RUN go get goteway
RUN go build ./cmd/server/main.go

CMD ["/app/main"]
