install:
	go mod tidy && go get goteway

run:
	go run ./cmd/server/main.go

build:
	go build ./cmd/server/main.go


# install go install github.com/cespare/reflex@latest
watch:
	ulimit -n 1000 
	reflex -s -r '\.go$$' make run

opencode:
	docker exec -it opencode opencode
test:
	go test -v ./...

test-coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out
