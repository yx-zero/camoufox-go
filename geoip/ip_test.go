package geoip

import "testing"

func TestProxyParts(t *testing.T) {
	tests := []struct {
		server     string
		wantScheme string
		wantHost   string
		wantPort   int
	}{
		{"socks5://1.2.3.4:1080", "socks5", "1.2.3.4", 1080},
		{"1.2.3.4:8080", "http", "1.2.3.4", 8080},
		{"proxy.example.com", "http", "proxy.example.com", 0},
		{"http://proxy.example.com:3128", "http", "proxy.example.com", 3128},
	}
	for _, tc := range tests {
		scheme, host, port, err := Proxy{Server: tc.server}.Parts()
		if err != nil {
			t.Errorf("Parts(%q): unexpected error: %v", tc.server, err)
			continue
		}
		if scheme != tc.wantScheme || host != tc.wantHost || port != tc.wantPort {
			t.Errorf("Parts(%q) = (%q, %q, %d), want (%q, %q, %d)",
				tc.server, scheme, host, port, tc.wantScheme, tc.wantHost, tc.wantPort)
		}
	}
}

func TestProxyURLString(t *testing.T) {
	tests := []struct {
		proxy Proxy
		want  string
	}{
		{Proxy{Server: "1.2.3.4:8080"}, "http://1.2.3.4:8080"},
		{Proxy{Server: "socks5://1.2.3.4:1080", Username: "u", Password: "p"}, "socks5://u:p@1.2.3.4:1080"},
		{Proxy{Server: "proxy.example.com:3128", Username: "u"}, "http://u@proxy.example.com:3128"},
	}
	for _, tc := range tests {
		if got := tc.proxy.URLString(); got != tc.want {
			t.Errorf("URLString(%+v) = %q, want %q", tc.proxy, got, tc.want)
		}
	}
}

func TestValidIP(t *testing.T) {
	if !ValidIPv4("8.8.8.8") {
		t.Error("ValidIPv4(8.8.8.8) = false")
	}
	if ValidIPv4("not-an-ip") {
		t.Error("ValidIPv4(not-an-ip) = true")
	}
	if !ValidIPv6("2001:4860:4860::8888") {
		t.Error("ValidIPv6(2001:4860:4860::8888) = false")
	}
	if ValidIPv6("8.8.8.8") {
		t.Error("ValidIPv6(8.8.8.8) = true")
	}
}

func TestGeolocationConfigKeys(t *testing.T) {
	g := Geolocation{
		Longitude: 1.5,
		Latitude:  2.5,
		Timezone:  "Europe/Paris",
	}
	g.Locale.Language = "fr"
	g.Locale.Region = "FR"

	cfg := g.ConfigKeys()
	if cfg["geolocation:longitude"] != 1.5 || cfg["geolocation:latitude"] != 2.5 {
		t.Errorf("ConfigKeys coords = %+v", cfg)
	}
	if cfg["timezone"] != "Europe/Paris" {
		t.Errorf("ConfigKeys timezone = %v", cfg["timezone"])
	}
	if cfg["locale:language"] != "fr" || cfg["locale:region"] != "FR" {
		t.Errorf("ConfigKeys locale = %+v", cfg)
	}
	if _, ok := cfg["geolocation:accuracy"]; ok {
		t.Errorf("ConfigKeys should omit accuracy when 0")
	}

	g.Accuracy = 50
	if g.ConfigKeys()["geolocation:accuracy"] != 50.0 {
		t.Errorf("ConfigKeys should include accuracy when > 0")
	}
}

func TestGetRepoByName(t *testing.T) {
	r, err := getRepoByName("")
	if err != nil || r.Name != defaultGeoIPName {
		t.Errorf("getRepoByName(\"\") = %+v, %v", r, err)
	}
	if _, err := getRepoByName("nonexistent"); err == nil {
		t.Errorf("getRepoByName(nonexistent): expected error")
	}
	aio, err := getRepoByName("GeoIP AIO by daijro")
	if err != nil || !aio.isCombined() {
		t.Errorf("AIO repo should be combined: %+v, %v", aio, err)
	}
}
