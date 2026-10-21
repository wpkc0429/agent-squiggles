// Package store holds the product catalog.
package store

import "fmt"

// Item is a product in the catalog.
type Item struct {
	ID    string
	Name  string
	Cents int
}

// Store is an in-memory catalog.
type Store struct {
	items map[string]Item
}

// New returns a store seeded with a few items.
func New() *Store {
	return &Store{items: map[string]Item{
		"apple":  {ID: "apple", Name: "Apple", Cents: 120},
		"banana": {ID: "banana", Name: "Banana", Cents: 80},
		"cherry": {ID: "cherry", Name: "Cherry", Cents: 450},
	}}
}

// Get returns the item with the given ID.
func (s *Store) Get(id string) (Item, error) {
	item, ok := s.items[id]
	if !ok {
		return Item{}, fmt.Errorf("store: no item %q", id)
	}
	return item, nil
}
