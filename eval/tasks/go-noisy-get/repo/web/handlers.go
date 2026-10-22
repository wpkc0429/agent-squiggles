// Package web serves the shop over HTTP.
package web

import (
	"fmt"
	"net/http"

	"example.com/shop/cache"
	"example.com/shop/config"
	"example.com/shop/inventory"
	"example.com/shop/pricing"
	"example.com/shop/stock"
)

// Server holds the handlers' dependencies.
type Server struct {
	Inv   *inventory.Inventory
	Cfg   config.Values
	Cache *cache.Cache
}

// Item renders one item: /item?sku=A1
func (s *Server) Item(w http.ResponseWriter, r *http.Request) {
	sku := r.URL.Query().Get("sku")
	if cached, ok := s.Cache.Get(sku); ok {
		fmt.Fprint(w, cached)
		return
	}
	item, ok := s.Inv.Get(sku)
	if !ok {
		http.NotFound(w, r)
		return
	}
	out := fmt.Sprintf("%s (%s %d)", item.Name, s.Cfg.Get("currency"), item.Cents)
	s.Cache.Put(sku, out)
	fmt.Fprint(w, out)
}

// Quote prices an order line: /quote?sku=A1&qty=2
func (s *Server) Quote(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var qty int
	fmt.Sscan(q.Get("qty"), &qty)
	cents, ok := pricing.Price(s.Inv, s.Cfg, q.Get("sku"), qty)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("X-Region", s.Cfg.Get("region"))
	fmt.Fprintf(w, "%d", cents)
}

// LowStock lists items to reorder: /low?sku=A1&sku=B2
func (s *Server) LowStock(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Admin") != "1" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	fmt.Fprint(w, stock.Low(s.Inv.Get, s.Cfg, r.URL.Query()["sku"]))
}
