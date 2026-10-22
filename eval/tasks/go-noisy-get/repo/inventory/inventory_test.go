package inventory

import "testing"

func TestGet(t *testing.T) {
	inv := New()
	if item, ok := inv.Get("A1"); !ok || item.Name != "Kettle" {
		t.Fatalf("Get(A1) = %+v, %v", item, ok)
	}
	if _, ok := inv.Get("Z9"); ok {
		t.Fatal("Get(Z9) should not find anything")
	}
}
