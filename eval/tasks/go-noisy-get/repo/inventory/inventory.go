// Package inventory tracks stock.
package inventory

// Item is a stocked product.
type Item struct {
	SKU   string
	Name  string
	Cents int
	Stock int
}

// Inventory is an in-memory stock list.
type Inventory struct {
	items map[string]Item
}

// New returns an inventory with a few items.
func New() *Inventory {
	return &Inventory{items: map[string]Item{
		"A1": {SKU: "A1", Name: "Kettle", Cents: 2500, Stock: 3},
		"B2": {SKU: "B2", Name: "Teapot", Cents: 1800, Stock: 0},
		"C3": {SKU: "C3", Name: "Mug", Cents: 600, Stock: 12},
	}}
}

// Get returns the item with the given SKU and whether it exists.
func (inv *Inventory) Get(sku string) (Item, bool) {
	item, ok := inv.items[sku]
	return item, ok
}
