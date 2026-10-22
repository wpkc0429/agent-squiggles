// Package pricing computes prices.
package pricing

import (
	"strconv"

	"example.com/shop/config"
	"example.com/shop/inventory"
)

// Catalog is what pricing needs from the inventory.
type Catalog interface {
	Get(sku string) (inventory.Item, bool)
}

// Price returns the price of quantity units of sku, in cents.
func Price(c Catalog, cfg config.Values, sku string, quantity int) (int, bool) {
	item, ok := c.Get(sku)
	if !ok {
		return 0, false
	}
	discount := 0
	if d, err := strconv.Atoi(cfg.Get("discount_pct")); err == nil {
		discount = d
	}
	return item.Cents * quantity * (100 - discount) / 100, true
}
