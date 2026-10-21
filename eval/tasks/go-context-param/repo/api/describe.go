// Package api renders catalog data for clients.
package api

import (
	"fmt"

	"example.com/shop/store"
)

// Lookup is the part of the store the API needs.
type Lookup interface {
	Get(id string) (store.Item, error)
}

// Describe returns a one-line description of an item.
func Describe(l Lookup, id string) string {
	item, err := l.Get(id)
	if err != nil {
		return "unknown item"
	}
	return fmt.Sprintf("%s: $%d.%02d", item.Name, item.Cents/100, item.Cents%100)
}
