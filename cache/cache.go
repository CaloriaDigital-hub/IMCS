package cache

import (
	"os"
	"time"
)



func New(dir string) *Cache {
	c := &Cache{
		items: make(map[string]Item),
		storageDir: dir,
	}


	if err := os.MkdirAll(dir, 0755); err != nil {
		panic("Cannot create storage directory: " + err.Error())
	}

	go c.startJanitor()

	return c

	
}

func (c *Cache) Set(key, value string, duration time.Duration) {

	var expiration int64

	if duration > 0 {
		expiration = time.Now().Add(duration).UnixNano()
	}
	c.mu.Lock()
	defer c.mu.Unlock()


	c.items[key] = Item{
		Value: value,
		Created: time.Now(),
		Expiration: expiration,
		
	}
}

func (c *Cache) Get(key string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	item, found := c.items[key]

	if !found {
		return "", false
	}

	if item.Expiration > 0 {
		if time.Now().UnixNano() > item.Expiration {
			return "", false
		}
	}

	return item.Value, true
}

func (c *Cache) Delete(key string) {
	c.mu.Lock()

	defer c.mu.Unlock()


	delete(c.items, key)

	

	
}