package main

import (
	"fmt"
	"os"

	"example.com/shop/api"
	"example.com/shop/pricing"
	"example.com/shop/store"
)

func main() {
	s := store.New()
	fmt.Println(api.Describe(s, "cherry"))
	total, err := pricing.Total(s, os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("total cents:", total)
}
