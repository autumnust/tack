.PHONY: test build check release clean

BINARY := tack
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")

test:
	go test ./internal/tui/ -v -count=1

build: test
	go build -o $(BINARY) .

check: test build

release: check
ifndef VERSION
	$(error VERSION is required. Usage: make release VERSION=v0.3)
endif
	git tag -a $(VERSION) -m "Release $(VERSION)"
	git push
	git push --tags
	@echo "Released $(VERSION)"

clean:
	rm -f $(BINARY)
