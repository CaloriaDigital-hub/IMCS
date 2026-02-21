package janitor

import (
	"imcs/internal/storage/cache"

)


type Janitor struct {
	cache 	*storage.Cache
	stopCh 	chan struct{}

}