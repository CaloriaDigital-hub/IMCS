package handler

import (
	"bufio"
	"fmt"
	"imcs/cache"
	"net"
	"strings"

	 "imcs/handler/cmd"
)


func HandleConnection(conn net.Conn, c *cache.Cache) {
	defer conn.Close()

	fmt.Println("new connect", conn.RemoteAddr().String())

	scanner := bufio.NewScanner(conn)

	for scanner.Scan() {

		text := strings.TrimSpace(scanner.Text())

		if text == "" {
			continue
		}

		parts := strings.Fields(text)

		cmdName := strings.ToUpper(parts[0])

		args := parts[1:]

		commandFunc, exists := cmd.Handlers[cmdName]


		if exists {
			response := commandFunc(args, c)
			conn.Write(response)
		} else {
			conn.Write([]byte("ERR unknown command\n"))
		}

		

		


		
	}

	

	if err := scanner.Err(); err != nil {
		fmt.Println("Connection error: ", err)
	}


	fmt.Println("Client disconnected: ",conn.RemoteAddr().String())

	

	

	

	
}

