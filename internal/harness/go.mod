module github.com/roachadam/mimicry/internal/harness

go 1.27.3

require (
	github.com/jonboulle/clockwork v0.5.0
	github.com/quic-go/quic-go v0.64.1
	github.com/roachadam/mimicry v0.0.0-00010101000000-000000000000
	github.com/roachadam/utls v0.0.0-20261025174743-24d1f83b2f8a
	golang.org/x/crypto v0.58.0
	golang.org/x/net v0.61.0
)

require (
	github.com/klauspost/compress v1.20.1 // indirect
	github.com/molecule-man/go-brrr v1.2.0 // indirect
	github.com/quic-go/qpack v0.6.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/text v0.43.0 // indirect
)

// The harness is never imported by others, so this replace only affects it.
replace github.com/roachadam/mimicry => ../../
