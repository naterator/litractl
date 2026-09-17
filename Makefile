VERSION ?= dev

.PHONY: build test vet test-hardware clean

build:
	go build -trimpath -ldflags '-s -w -X main.version=$(VERSION)' -o litracli ./cmd/litracli

test:
	go test ./...

vet:
	go vet ./...

# Explicitly opt in: briefly changes attached lights and restores their settings.
test-hardware:
	go test -tags=integration -run '^TestHardware$$' -count=1 -v ./internal/litra

clean:
	rm -f litracli litracli.exe
