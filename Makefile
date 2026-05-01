default: run

run:
	@go run routes.go app.go version.go

dist:
	rm -rf app
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o app -ldflags "-s -w -X main.Version=$(VERSION)" -trimpath routes.go app.go version.go

docker:
	docker build -t ghcr.io/entriq/entriq:latest .
