.PHONY: build test clean install

BINARY  := mnemo
VERSION := 0.1.0

build:
	go build -o $(BINARY) .

test:
	go test ./...

clean:
	rm -f $(BINARY)

install: build
	sudo mv $(BINARY) /usr/local/bin/$(BINARY)
	@echo "Installed $(BINARY) v$(VERSION)"

vet:
	go vet ./...

fmt:
	gofmt -w .

lint: vet fmt
