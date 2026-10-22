package main

import (
	"log"
	"net/http"
	"os"

	"example.com/shop/cache"
	"example.com/shop/config"
	"example.com/shop/inventory"
	"example.com/shop/web"
)

func main() {
	s := &web.Server{Inv: inventory.New(), Cfg: config.Default(), Cache: cache.New()}
	if item, ok := s.Inv.Get(os.Getenv("WARM_SKU")); ok {
		log.Printf("warm: %s", item.Name)
	}
	http.HandleFunc("/item", s.Item)
	http.HandleFunc("/quote", s.Quote)
	http.HandleFunc("/low", s.LowStock)
	log.Fatal(http.ListenAndServe(":8080", nil))
}
