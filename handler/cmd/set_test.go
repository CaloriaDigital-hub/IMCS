package cmd

import (
	"imcs/cache"
	"testing"
)

func TestHandleSet(t *testing.T) {
	c := cache.New("./test-data")

	args := []string{"mykey", "100"}

	response := handleSet(args, c)

	expected := "OK\n"
	if string(response) != expected {
		t.Errorf("Ожидали %s, получили %s", expected, string(response))
	}

	val, found := c.Get("mykey")
	if !found {
		t.Error("Ключ не был сохранен в кэше")
	}
	if val != "100" {
		t.Errorf("В кэше лежит %s, а ждали 100", val)
	}
}