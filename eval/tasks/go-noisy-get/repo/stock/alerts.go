// Package stock reports low stock.
package stock

import (
	"strconv"

	"example.com/shop/config"
	"example.com/shop/inventory"
)

// Lookup finds an item by SKU.
type Lookup func(sku string) (inventory.Item, bool)

// Low lists the SKUs whose stock is at or below the configured threshold.
func Low(lookup Lookup, cfg config.Values, skus []string) []string {
	threshold, _ := strconv.Atoi(cfg.Get("low_stock"))
	var low []string
	for _, sku := range skus {
		if item, ok := lookup(sku); ok && item.Stock <= threshold {
			low = append(low, sku)
		}
	}
	return low
}
