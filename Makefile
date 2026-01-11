default: run

run:
	@go run routes.go app.go

dist:
	rm -rf app
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o app -ldflags "-s -w" -trimpath routes.go app.go

patch:
	patch -p1 < config/release.patch

patch-reverse:
	patch -Rp1 < config/release.patch