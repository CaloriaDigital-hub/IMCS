package cache

import (
	"time"
	"errors"
	"imcs/persistence/AOF"
)

var ErrKeyExist = errors.New("key already exists")

func New(dir string) *Cache {

	aof, err := AOF.NewAOF(dir)

	if err != nil {
		panic("Cannot open AOF: " + err.Error())
	}
	c := &Cache{
		items: make(map[string]Item),
		persister: aof,
	}


	err = c.persister.Read(func(cmd string, key string, value string, expire int64) {

		switch cmd {
		case "SET":
			if expire > 0 && expire <time.Now().UnixNano() {
				return 
			}

			c.items[key] = Item{
				Value: value,
				Expiration: expire,
				Created: time.Now(),
			}
		case "DEL":

			delete(c.items, key)


		}
	})

	if err != nil {
		println("Error restoring AOF: ", err.Error())
	}

	go c.startJanitor()

	return c

	
}



func (c *Cache) Set(key, value string, duration time.Duration, nx bool) error {

	var expiration int64

	if duration > 0 {
		expiration = time.Now().Add(duration).UnixNano()
	}
	c.mu.Lock()
	defer c.mu.Unlock()


	if nx {
		item, found := c.items[key]

		if found {
			if item.Expiration == 0 || item.Expiration > time.Now().UnixNano() {
				return ErrKeyExist
			}
		}
	}


	c.items[key] = Item{
		Value: value,
		Created: time.Now(),
		Expiration: expiration,
		
	}

	go c.persister.Write(AOF.WriteInput{
		Cmd: "SET",
		Key: key,
		Value: value,
		TTL: duration,
	})

	return nil

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
	delete(c.items, key)
	defer c.mu.Unlock()



	go c.persister.Write(AOF.WriteInput{
		Cmd: "DEL",
		Key: key,
	})
	

	

	
}