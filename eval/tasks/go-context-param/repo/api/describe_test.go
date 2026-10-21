package api

import (
	"testing"

	"example.com/shop/store"
)

func TestDescribe(t *testing.T) {
	if got := Describe(store.New(), "banana"); got != "Banana: $0.80" {
		t.Fatalf("Describe = %q", got)
	}
}
