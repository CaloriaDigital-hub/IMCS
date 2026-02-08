package main

import (
	"flag" 
	"fmt"
	"imcs/cache"
	"imcs/handler"
	"log"
	"net"
)

const TYPE = "tcp"

func main() {
	
	port := flag.String("port", ":8080", "Network port")
	dir := flag.String("dir", "./cache-files", "Directory for cache dumps")
	
	flag.Parse()

	c := cache.New(*dir)
	
	if err := c.LoadFromFile("cache.dump"); err != nil {
		log.Println("Warning: Failed to load cache:", err)
	}

	fmt.Printf("Starting server on port %s... Storage: %s\n", *port, *dir)

	ln, err := net.Listen(TYPE, *port)
	if err != nil {
		log.Fatal(err)
	}
	defer ln.Close()

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Println("Connection error:", err)
			continue
		}
		go handler.HandleConnection(conn, c)
	}
}