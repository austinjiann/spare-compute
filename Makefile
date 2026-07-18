.PHONY: check fmt test race vet

check: fmt vet test race

fmt:
	@test -z "$$(gofmt -l .)" || (gofmt -d . && exit 1)

vet:
	go vet ./...

test:
	go test ./...

race:
	go test -race ./...
