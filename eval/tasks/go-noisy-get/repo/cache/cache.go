// Package cache is a tiny string cache.
package cache

// Cache maps keys to rendered strings.
type Cache struct{ m map[string]string }

// New returns an empty cache.
func New() *Cache { return &Cache{m: map[string]string{}} }

// Get returns a cached value.
func (c *Cache) Get(key string) (string, bool) {
	v, ok := c.m[key]
	return v, ok
}

// Put stores a value.
func (c *Cache) Put(key, value string) { c.m[key] = value }
