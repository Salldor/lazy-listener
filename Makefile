build:
	go build -mod=vendor -o lazy-listener ./cmd/

run: build
	./lazy-listener -model ./models/ggml-medium.bin

.PHONY: build run
