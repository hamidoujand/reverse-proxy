run:
	ENVIRONMENT=development HOST=0.0.0.0:8080 TARGET_SERVER=http://localhost:9000 go run cmd/main.go

tidy:
	go mod tidy 
	go mod vendor

tests:
	ENVIRONMENT=development go test ./... -v
