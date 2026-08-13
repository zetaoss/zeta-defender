.PHONY: checks go-checks docker-check

checks: go-checks docker-check

go-checks:
	test -z "$$(gofmt -l .)"
	go mod tidy -diff
	go vet ./...
	go test -race -count=1 ./...

docker-check:
	docker build --tag zeta-defender:ci .
