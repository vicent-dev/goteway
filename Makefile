install:
	go mod tidy

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

# mint a one time registration token
genregtoken:
	go run ./cmd/admin/genregtoken -issued-by cli

test:
	go test -v ./...

test-coverage:
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out
