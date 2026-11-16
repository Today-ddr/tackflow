package events

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var ErrUnsafeURL = errors.New("webhook URL is not allowed")

type URLPolicy struct {
	allowPrivate bool
	lookup       func(context.Context, string) ([]net.IPAddr, error)
	dial         func(context.Context, string, string) (net.Conn, error)
}

func NewURLPolicy(allowPrivate bool) *URLPolicy {
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	return &URLPolicy{
		allowPrivate: allowPrivate,
		lookup:       net.DefaultResolver.LookupIPAddr,
		dial:         dialer.DialContext,
	}
}

func (policy *URLPolicy) Validate(ctx context.Context, raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil || len(raw) > 2048 || strings.TrimSpace(raw) != raw || strings.Contains(raw, "#") ||
		parsed.Hostname() == "" || parsed.User != nil || parsed.Opaque != "" ||
		(parsed.Scheme != "https" && (!policy.allowPrivate || parsed.Scheme != "http")) {
		return nil, ErrUnsafeURL
	}
	if port := parsed.Port(); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value < 1 || value > 65535 {
			return nil, ErrUnsafeURL
		}
	} else if strings.HasSuffix(parsed.Host, ":") {
		return nil, ErrUnsafeURL
	}
	if _, err := policy.resolve(ctx, parsed.Hostname()); err != nil {
		return nil, err
	}
	return parsed, nil
}

func (policy *URLPolicy) resolve(ctx context.Context, host string) ([]net.IPAddr, error) {
	var addresses []net.IPAddr
	if literal := net.ParseIP(host); literal != nil {
		addresses = []net.IPAddr{{IP: literal}}
	} else {
		var err error
		addresses, err = policy.lookup(ctx, host)
		if err != nil || len(addresses) == 0 {
			return nil, ErrUnsafeURL
		}
	}
	for _, address := range addresses {
		parsed, valid := netip.AddrFromSlice(address.IP)
		if !valid || address.Zone != "" || !policy.allowedIP(parsed.Unmap()) {
			return nil, ErrUnsafeURL
		}
	}
	return addresses, nil
}

var reservedNetworks = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.0.2.0/24"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("64:ff9b::/96"),
	netip.MustParsePrefix("64:ff9b:1::/48"),
	netip.MustParsePrefix("100::/64"),
	netip.MustParsePrefix("2001::/23"),
	netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("fec0::/10"),
}

func (policy *URLPolicy) allowedIP(address netip.Addr) bool {
	if address.IsLoopback() || address.IsPrivate() || address.IsLinkLocalUnicast() {
		return policy.allowPrivate
	}
	if !address.IsGlobalUnicast() {
		return false
	}
	for _, prefix := range reservedNetworks {
		if prefix.Contains(address) {
			return false
		}
	}
	return true
}

func (policy *URLPolicy) dialContext(ctx context.Context, network, target string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		return nil, ErrUnsafeURL
	}
	addresses, err := policy.resolve(ctx, host)
	if err != nil {
		return nil, err
	}
	var dialErr error
	for _, address := range addresses {
		connection, err := policy.dial(ctx, network, net.JoinHostPort(address.IP.String(), port))
		if err == nil {
			return connection, nil
		}
		dialErr = err
	}
	return nil, dialErr
}
