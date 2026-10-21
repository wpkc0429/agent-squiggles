// Package pricing computes order totals.
package pricing

import "example.com/shop/store"

// Total returns the price of the items in cents.
func Total(s *store.Store, ids []string) (int, error) {
	total := 0
	for _, id := range ids {
		item, err := s.Get(id)
		if err != nil {
			return 0, err
		}
		total += item.Cents
	}
	return total, nil
}
