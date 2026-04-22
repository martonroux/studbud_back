.PHONY: build run test test-pkg vet fmt tidy db-setup

build:
	go build ./...

run:
	./launch_app.sh

test:
	ENV=test go test ./... -p 1 -count=1

test-pkg:
	ENV=test go test ./$(PKG)/... -p 1 -count=1 -v

vet:
	go vet ./...

fmt:
	gofmt -s -w .

tidy:
	go mod tidy

db-setup:
	./setup_db.sh
