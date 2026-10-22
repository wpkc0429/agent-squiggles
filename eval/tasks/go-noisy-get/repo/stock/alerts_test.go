package stock

import (
	"reflect"
	"testing"

	"example.com/shop/config"
	"example.com/shop/inventory"
)

func TestLow(t *testing.T) {
	inv := inventory.New()
	got := Low(inv.Get, config.Default(), []string{"A1", "B2", "C3", "Z9"})
	if want := []string{"B2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Low = %v, want %v", got, want)
	}
}
