package cache

import "time"

func(c *Cache) startJanitor() {
	ticker := time.NewTicker(time.Second)

	defer ticker.Stop()


	for {
		<-ticker.C

		c.deleteExpired()

		
	}
}

func (c *Cache) deleteExpired() {
	c.mu.Lock()

	defer c.mu.Unlock()

	now := time.Now().UnixNano()

	for key, item := range c.items {
		if item.Expiration > 0 && now > item.Expiration {
			delete(c.items, key)
		}
	}
}