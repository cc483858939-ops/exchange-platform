package config

import (
	"reflect"
	"testing"
)

func TestTrustedProxyCIDRsEmptyValueReturnsNil(t *testing.T) {
	t.Setenv("TRUSTED_PROXY_CIDRS", "")
	proxies, err := TrustedProxyCIDRs()
	if err != nil {
		t.Fatal(err)
	}
	if proxies != nil {
		t.Fatalf("proxies=%v, want nil", proxies)
	}
}

func TestTrustedProxyCIDRsNormalizesAndDeduplicates(t *testing.T) {
	t.Setenv("TRUSTED_PROXY_CIDRS", " 10.42.0.0/16, 192.168.1.10, 2001:db8::/64, 192.168.1.10/32, 2001:db8::/64 ")
	proxies, err := TrustedProxyCIDRs()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"10.42.0.0/16", "192.168.1.10/32", "2001:db8::/64"}
	if !reflect.DeepEqual(proxies, want) {
		t.Fatalf("proxies=%v, want %v", proxies, want)
	}
}

func TestTrustedProxyCIDRsNormalizesBareIPv6(t *testing.T) {
	t.Setenv("TRUSTED_PROXY_CIDRS", "2001:0db8:0000:0000:0000:0000:0000:0001")
	proxies, err := TrustedProxyCIDRs()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"2001:db8::1/128"}
	if !reflect.DeepEqual(proxies, want) {
		t.Fatalf("proxies=%v, want %v", proxies, want)
	}
}

func TestTrustedProxyCIDRsRejectsInvalidAndTrustAllValues(t *testing.T) {
	for _, value := range []string{"not-an-ip", "example.com", "*", "0.0.0.0/0", "::/0"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("TRUSTED_PROXY_CIDRS", value)
			if _, err := TrustedProxyCIDRs(); err == nil {
				t.Fatalf("TrustedProxyCIDRs(%q) unexpectedly succeeded", value)
			}
		})
	}
}

func TestCORSAllowedOriginsDefaultsToLocalDevelopment(t *testing.T) {
	t.Setenv("CORS_ALLOWED_ORIGINS", "")
	origins, err := CORSAllowedOrigins()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"http://localhost:5173", "http://127.0.0.1:5173"}
	if !reflect.DeepEqual(origins, want) {
		t.Fatalf("origins=%v, want %v", origins, want)
	}
}

func TestCORSAllowedOriginsTrimsDiscardsAndDeduplicates(t *testing.T) {
	t.Setenv("CORS_ALLOWED_ORIGINS", " https://app.example.com, ,https://www.example.com, https://app.example.com ")
	origins, err := CORSAllowedOrigins()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"https://app.example.com", "https://www.example.com"}
	if !reflect.DeepEqual(origins, want) {
		t.Fatalf("origins=%v, want %v", origins, want)
	}
}

func TestCORSAllowedOriginsRejectsWildcardAndEmptyList(t *testing.T) {
	for _, value := range []string{"*", " , , "} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("CORS_ALLOWED_ORIGINS", value)
			if _, err := CORSAllowedOrigins(); err == nil {
				t.Fatalf("CORSAllowedOrigins(%q) unexpectedly succeeded", value)
			}
		})
	}
}
