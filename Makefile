.PHONY: build run clean cross

APP     := claude-relay
VERSION := 1.0.0

build:
	go build -ldflags="-s -w" -o $(APP) .

run: build
	./$(APP)

# Cross-compile for common targets
cross:
	GOOS=linux   GOARCH=amd64 go build -ldflags="-s -w" -o dist/$(APP)-linux-amd64 .
	GOOS=linux   GOARCH=arm64 go build -ldflags="-s -w" -o dist/$(APP)-linux-arm64 .
	GOOS=darwin  GOARCH=amd64 go build -ldflags="-s -w" -o dist/$(APP)-darwin-amd64 .
	GOOS=darwin  GOARCH=arm64 go build -ldflags="-s -w" -o dist/$(APP)-darwin-arm64 .
	GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o dist/$(APP)-windows-amd64.exe .

clean:
	rm -rf $(APP) dist/
