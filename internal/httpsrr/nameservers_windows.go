package httpsrr

import (
	"errors"
	"net"
	"unsafe"

	"golang.org/x/sys/windows"
)

// systemNameservers returns the DNS servers of the interfaces that are up
// and have a gateway, on port 53, or defaultNameservers without any, as
// Go's resolver reads them on Windows (net's dnsReadConfig).
func systemNameservers() []string {
	adapters, err := adapterAddresses()
	if err != nil {
		return defaultNameservers
	}

	var servers []string

	for _, aa := range adapters {
		if aa.OperStatus != windows.IfOperStatusUp || aa.FirstGatewayAddress == nil {
			continue
		}

		for dns := aa.FirstDnsServerAddress; dns != nil; dns = dns.Next {
			ip := dns.Address.IP()
			// fec0::/10 are the deprecated site-local anycast addresses
			// Windows sets when no other IPv6 DNS server is (RFC 3879).
			if ip == nil || len(ip) == net.IPv6len && ip[0] == 0xfe && ip[1]&0xc0 == 0xc0 {
				continue
			}

			servers = append(servers, net.JoinHostPort(ip.String(), "53"))
		}
	}

	if len(servers) == 0 {
		return defaultNameservers
	}

	return servers
}

// adapterAddresses returns the system's network adapters, as net's
// function of that name on Windows does.
func adapterAddresses() ([]*windows.IpAdapterAddresses, error) {
	const flags = windows.GAA_FLAG_INCLUDE_PREFIX | windows.GAA_FLAG_INCLUDE_GATEWAYS

	size := uint32(15000) // the recommended initial size

	var buf []byte

	for {
		buf = make([]byte, size)

		err := windows.GetAdaptersAddresses(windows.AF_UNSPEC, flags, 0, first(buf), &size)
		if err == nil {
			if size == 0 {
				return nil, nil
			}

			break
		}

		if !errors.Is(err, windows.ERROR_BUFFER_OVERFLOW) || size <= uint32(len(buf)) {
			return nil, err
		}
	}

	var adapters []*windows.IpAdapterAddresses

	for aa := first(buf); aa != nil; aa = aa.Next {
		adapters = append(adapters, aa)
	}

	return adapters, nil
}

// first is the adapter list GetAdaptersAddresses writes at the start of
// buf.
func first(buf []byte) *windows.IpAdapterAddresses {
	return (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0])) //nolint:gosec // G103: GetAdaptersAddresses fills buf with linked IP_ADAPTER_ADDRESSES structures, as net reads them
}
