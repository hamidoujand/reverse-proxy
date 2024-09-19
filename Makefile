run:
	HOST=0.0.0.0:8080 go run cmd/main.go

tidy:
	go mod tidy 
	go mod vendor