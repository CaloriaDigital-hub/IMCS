package main

import (
	
	"net"

	"imcs/cache"
	"imcs/handler"
	"log"
)

const (
	HOST = "localhost"
	PORT = ":8080"
	TYPE = "tcp"
)




func main() {

	c := cache.New()
	c.LoadFromFile("cache.dump")
	ln, err := net.Listen(TYPE, PORT)

	if err != nil {
		log.Fatal(err)
	}



	defer ln.Close()


	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Fatal(err)			
			continue
		}

		go handler.HandleConnection(conn, c)

		
	}
}

