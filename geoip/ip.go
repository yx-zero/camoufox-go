// Package geoip provides IP geolocation, timezone, and locale resolution given
// an IP address, ported from the Python camoufox library (ip.py +
// geolocation.py). It downloads and caches MaxMind .mmdb databases and reads
// them with a pure-Go reader so the whole package builds with CGO_ENABLED=0.
package geoip

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Proxy stores proxy information. Ports ip.Proxy.
type Proxy struct {
	Server   string
	Username string
	Password string
	Bypass   string
}

// parseServerRe matches an optional scheme, a host, and an optional :port.
// Ports the regex in Proxy.parse_server.
var parseServerRe = regexp.MustCompile(`^(?:(\w+)://)?(.*?)(?::(\d+))?$`)

// Parts parses the proxy server string into scheme, host, and port.
// The scheme defaults to "http" when absent; port is 0 when absent.
// Ports Proxy.parse_server.
func (p Proxy) Parts() (scheme, host string, port int, err error) {
	m := parseServerRe.FindStringSubmatch(p.Server)
	if m == nil {
		return "", "", 0, fmt.Errorf("invalid proxy server: %s", p.Server)
	}
	scheme = m[1]
	host = m[2]
	if scheme == "" {
		scheme = "http"
	}
	if m[3] != "" {
		port, err = strconv.Atoi(m[3])
		if err != nil {
			return "", "", 0, fmt.Errorf("invalid proxy port: %s", m[3])
		}
	}
	return scheme, host, port, nil
}

// URLString builds a proxy URL string, embedding user:pass@ credentials when
// present. The scheme defaults to "http". Ports Proxy.as_string.
func (p Proxy) URLString() string {
	scheme, host, port, err := p.Parts()
	if err != nil || scheme == "" {
		scheme = "http"
	}

	var b strings.Builder
	b.WriteString(scheme)
	b.WriteString("://")
	if p.Username != "" {
		b.WriteString(p.Username)
		if p.Password != "" {
			b.WriteString(":")
			b.WriteString(p.Password)
		}
		b.WriteString("@")
	}
	b.WriteString(host)
	if port != 0 {
		b.WriteString(":")
		b.WriteString(strconv.Itoa(port))
	}
	return b.String()
}

var (
	ipv4Re = regexp.MustCompile(`^(?:[0-9]{1,3}\.){3}[0-9]{1,3}$`)
	ipv6Re = regexp.MustCompile(`^(([0-9a-fA-F]{0,4}:){1,7}[0-9a-fA-F]{0,4})$`)
)

// ValidIPv4 reports whether s looks like an IPv4 address. Ports valid_ipv4.
func ValidIPv4(s string) bool {
	return ipv4Re.MatchString(s)
}

// ValidIPv6 reports whether s looks like an IPv6 address. Ports valid_ipv6.
func ValidIPv6(s string) bool {
	return ipv6Re.MatchString(s)
}

// validateIP returns an error when ip is neither a valid IPv4 nor IPv6 address.
// Ports validate_ip.
func validateIP(ip string) error {
	if !ValidIPv4(ip) && !ValidIPv6(ip) {
		return fmt.Errorf("invalid IP address: %s", ip)
	}
	return nil
}

// publicIPURLs is the ordered list of public IP echo services. Ports the URLS
// list in public_ip.
var publicIPURLs = []string{
	// Prefers IPv4
	"https://api.ipify.org",
	"https://checkip.amazonaws.com",
	"https://ipinfo.io/ip",
	// IPv4 & IPv6
	"https://icanhazip.com",
	"https://ifconfig.co/ip",
	"https://ipecho.net/plain",
}

// PublicIP fetches the public IP address by querying a list of echo services
// in order, returning the first valid IPv4/IPv6 response. Each request uses a
// 5s timeout and the optional proxy. Ports public_ip.
func PublicIP(proxy *Proxy) (string, error) {
	transport := &http.Transport{}
	if proxy != nil {
		proxyURL, err := url.Parse(proxy.URLString())
		if err != nil {
			return "", fmt.Errorf("invalid proxy: %w", err)
		}
		transport.Proxy = http.ProxyURL(proxyURL)
	}

	client := &http.Client{
		Timeout:   5 * time.Second,
		Transport: transport,
	}

	var lastErr error
	for _, u := range publicIPURLs {
		resp, err := client.Get(u)
		if err != nil {
			lastErr = err
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			lastErr = fmt.Errorf("%s returned status %d", u, resp.StatusCode)
			continue
		}
		ip := strings.TrimSpace(string(body))
		if err := validateIP(ip); err != nil {
			lastErr = err
			continue
		}
		return ip, nil
	}

	return "", fmt.Errorf("failed to get IP address: %w", lastErr)
}
