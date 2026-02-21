package cold

import (
	"os"
	"path/filepath"
)

/*

	Возможно в будущем конструктор будет раздут
	посему вынес в отдельный файл

*/


/*

	Странно называть это cold'ом,
	но другое название не придумал
	________________________________________________
	________________________________________________

	New создаёт "cold store" в указанной дирректории

*/
func New(dir string) (*Store, error) {
	dir = filepath.Join(dir, "cold")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	s := &Store{
		dir:	dir,
		index: 	make(map[string]entry),

	}


	if err := s.load(); err != nil {
		// просто начинаем с пустого
	}

	return s, nil
}