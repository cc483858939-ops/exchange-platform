package core

import (
	"net/http"
	"testing"
	"time"
)

func TestNewAPIServerHasResourceTimeouts(t *testing.T) {
	server := newAPIServer(":3000", http.NotFoundHandler())
	if server.ReadHeaderTimeout != 5*time.Second {
		t.Fatalf("ReadHeaderTimeout=%s, want %s", server.ReadHeaderTimeout, 5*time.Second)
	}
	if server.ReadTimeout != 60*time.Second {
		t.Fatalf("ReadTimeout=%s, want %s", server.ReadTimeout, 60*time.Second)
	}
	if server.WriteTimeout != 120*time.Second {
		t.Fatalf("WriteTimeout=%s, want %s", server.WriteTimeout, 120*time.Second)
	}
	if server.IdleTimeout != 120*time.Second {
		t.Fatalf("IdleTimeout=%s, want %s", server.IdleTimeout, 120*time.Second)
	}
	if server.MaxHeaderBytes != 1<<20 {
		t.Fatalf("MaxHeaderBytes=%d, want %d", server.MaxHeaderBytes, 1<<20)
	}
}
