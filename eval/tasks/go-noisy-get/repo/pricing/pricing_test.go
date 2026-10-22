package pricing

import (
	"testing"

	"example.com/shop/config"
	"example.com/shop/inventory"
)

func TestPrice(t *testing.T) {
	cfg := config.Default()
	cfg["discount_pct"] = "10"
	got, ok := Price(inventory.New(), cfg, "C3", 2)
	if !ok || got != 1080 {
		t.Fatalf("Price = %d, %v", got, ok)
	}
	if cfg.Get("currency") != "USD" {
		t.Fatal("unexpected currency")
	}
}
