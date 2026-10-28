module github.com/roachadam/mimicry/internal/bench

go 1.27.3

require (
	github.com/Noooste/azuretls-client v1.13.2
	github.com/Noooste/utls v1.3.20
	github.com/bogdanfinn/fhttp v0.7.1
	github.com/bogdanfinn/tls-client v1.17.1
	github.com/quic-go/quic-go v0.64.1
	github.com/roachadam/mimicry v0.0.0-00010101000000-000000000000
	github.com/sardanioss/httpcloak v1.7.3
)

require (
	github.com/Noooste/fhttp v1.0.15 // indirect
	github.com/Noooste/go-socks4 v0.0.2 // indirect
	github.com/Noooste/uquic-go v1.0.5 // indirect
	github.com/Noooste/websocket v1.0.3 // indirect
	github.com/aclements/go-moremath v0.0.0-20210112150236-f10218a38794 // indirect
	github.com/andybalholm/brotli v1.2.0 // indirect
	github.com/bdandy/go-errors v1.2.2 // indirect
	github.com/bdandy/go-socks4 v1.2.3 // indirect
	github.com/bogdanfinn/quic-go-utls v1.0.12-utls // indirect
	github.com/bogdanfinn/utls v1.8.2-barnius // indirect
	github.com/bogdanfinn/websocket v1.5.8-barnius // indirect
	github.com/cloudflare/circl v1.6.2 // indirect
	github.com/fatih/color v1.18.0 // indirect
	github.com/gaukas/clienthellod v0.4.2 // indirect
	github.com/gaukas/godicttls v0.0.4 // indirect
	github.com/google/gopacket v1.1.19 // indirect
	github.com/jonboulle/clockwork v0.5.0 // indirect
	github.com/klauspost/compress v1.20.1 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/miekg/dns v1.1.69 // indirect
	github.com/molecule-man/go-brrr v1.2.0 // indirect
	github.com/quic-go/qpack v0.6.0 // indirect
	github.com/refraction-networking/utls v1.8.1 // indirect
	github.com/roachadam/utls v0.0.0-20261028030754-3ef8aebd60b6 // indirect
	github.com/sardanioss/http v1.2.0 // indirect
	github.com/sardanioss/net v1.2.12 // indirect
	github.com/sardanioss/qpack v0.6.3 // indirect
	github.com/sardanioss/quic-go v1.2.30 // indirect
	github.com/sardanioss/udpbara v1.1.0 // indirect
	github.com/sardanioss/utls v1.10.5 // indirect
	github.com/tam7t/hpkp v0.0.0-20160821193359-2b70b4024ed5 // indirect
	golang.org/x/crypto v0.58.0 // indirect
	golang.org/x/mod v0.41.0 // indirect
	golang.org/x/net v0.61.0 // indirect
	golang.org/x/perf v0.0.0-20261009192801-be2c69fb417e // indirect
	golang.org/x/sync v0.24.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/text v0.43.0 // indirect
	golang.org/x/tools v0.51.0 // indirect
)

// The bench module is never imported by others, so this replace only affects it.
replace github.com/roachadam/mimicry => ../../

tool golang.org/x/perf/cmd/benchstat
