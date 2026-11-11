module github.com/roachadam/mimicry

go 1.27.4

// The Node SDK's dependencies (npm ci in bindings/node) can ship Go files
// (flatted's), which ./... would otherwise build, vet and lint.
ignore node_modules

require (
	github.com/bufbuild/protocompile v0.14.1
	github.com/jonboulle/clockwork v0.5.0
	github.com/klauspost/compress v1.20.2
	github.com/molecule-man/go-brrr v1.2.0
	github.com/roachadam/utls v1.8.3
	golang.org/x/crypto v0.58.0
	golang.org/x/net v0.61.0
	golang.org/x/sys v0.49.0
	google.golang.org/protobuf v1.36.12
)

require (
	golang.org/x/sync v0.24.0 // indirect
	golang.org/x/text v0.43.0 // indirect
)
