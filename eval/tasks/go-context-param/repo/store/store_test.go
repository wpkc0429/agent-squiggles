package store

import "testing"

func TestGet(t *testing.T) {
	s := New()
	item, err := s.Get("apple")
	if err != nil || item.Cents != 120 {
		t.Fatalf("Get(apple) = %+v, %v", item, err)
	}
	if _, err := s.Get("durian"); err == nil {
		t.Fatal("expected an error for a missing item")
	}
}
