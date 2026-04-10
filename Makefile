fmt:
	find . -name "*.go" -not -path "./vendor/*" -exec gofmt -w {} +

lint:
	go tool staticcheck ./...

vet:
	go vet ./...

hooks-install:
	go tool lefthook install
