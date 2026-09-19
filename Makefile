run:
	go run ./cmd/server/main.go

build:
	go build ./cmd/server/main.go


# install go install github.com/cespare/reflex@latest
watch:
	ulimit -n 1000 
	reflex -s -r '\.go$$' make run
