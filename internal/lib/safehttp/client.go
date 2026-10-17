package safehttp

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"syscall"
	"time"
)

var ErrPrivateAddress = errors.New("safehttp: private address")

// Control runs after DNS resolution, so a public hostname resolving to an internal address is refused too.
func NewClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{
		Timeout: timeout,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return fmt.Errorf("err split dial address: %w", err)
			}

			ip, err := netip.ParseAddr(host)
			if err != nil {
				return fmt.Errorf("err parse dial address: %w", err)
			}

			if ip = ip.Unmap(); !ip.IsGlobalUnicast() || ip.IsPrivate() {
				return ErrPrivateAddress
			}

			return nil
		},
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = dialer.DialContext

	return &http.Client{Timeout: timeout, Transport: transport}
}
