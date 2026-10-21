package pricing

import (
	"testing"

	"example.com/shop/store"
)

func TestTotal(t *testing.T) {
	got, err := Total(store.New(), []string{"apple", "banana"})
	if err != nil || got != 200 {
		t.Fatalf("Total = %d, %v", got, err)
	}
}
