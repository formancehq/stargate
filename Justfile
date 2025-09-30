set dotenv-load

default:
  @just --list

pre-commit: tidy generate lint
pc: pre-commit

lint:
    golangci-lint run --fix --build-tags it,local --timeout 5m
    
tidy:
    go mod tidy
    
generate:
    protoc --go_out=generated --go_opt=paths=source_relative --go-grpc_out=./internal/generated --go-grpc_opt=paths=source_relative stargate.proto
g: generate

tests:
    go test -race -covermode=atomic \
        -coverprofile coverage.txt \
        -tags it \
        ./...
    cat coverage.txt | grep -v debug.go | grep -v "/machine/" | grep -v "pb.go" > coverage2.txt
    mv coverage2.txt coverage.txt

release-local:
    @goreleaser release --nightly --skip=publish --clean

release-ci:
    @goreleaser release --nightly --clean

release:
    @goreleaser release --clean
