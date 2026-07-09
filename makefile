run:
	go run ./cmd/api/main.go

build:
	GOOS=linux GOARCH=amd64 go build -o campusassistant-api ./cmd/api/main.go


reset-db:
	go run ./scripts/reset_db.go
