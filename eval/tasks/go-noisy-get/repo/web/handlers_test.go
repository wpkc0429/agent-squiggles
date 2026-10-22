package web

import (
	"net/http/httptest"
	"testing"

	"example.com/shop/cache"
	"example.com/shop/config"
	"example.com/shop/inventory"
)

func server() *Server {
	return &Server{Inv: inventory.New(), Cfg: config.Default(), Cache: cache.New()}
}

func TestItem(t *testing.T) {
	rec := httptest.NewRecorder()
	server().Item(rec, httptest.NewRequest("GET", "/item?sku=A1", nil))
	if got := rec.Body.String(); got != "Kettle (USD 2500)" {
		t.Fatalf("body = %q", got)
	}
}

func TestQuote(t *testing.T) {
	rec := httptest.NewRecorder()
	server().Quote(rec, httptest.NewRequest("GET", "/quote?sku=C3&qty=3", nil))
	if got := rec.Body.String(); got != "1800" {
		t.Fatalf("body = %q", got)
	}
	if rec.Header().Get("X-Region") != "us" {
		t.Fatal("missing region header")
	}
}

func TestLowStock(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/low?sku=A1&sku=B2", nil)
	req.Header.Set("X-Admin", "1")
	server().LowStock(rec, req)
	if got := rec.Body.String(); got != "[B2]" {
		t.Fatalf("body = %q", got)
	}
}
