package ingest

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"time"
)

var errPrivateAddress = errors.New("ingest: private address")

// Control runs after DNS resolution, so a public hostname resolving to an internal address is refused too.
func safeClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{
		Timeout: timeout,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}

			ip, err := netip.ParseAddr(host)
			if err != nil {
				return err
			}

			if ip = ip.Unmap(); !ip.IsGlobalUnicast() || ip.IsPrivate() {
				return errPrivateAddress
			}

			return nil
		},
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = dialer.DialContext

	return &http.Client{Timeout: timeout, Transport: transport}
}
