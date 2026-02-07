package handler

import (
	"bufio"
	"fmt"
	"imcs/cache"
	
	"net"
	"strconv"
	"strings"
	"time"
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

		args := strings.Fields(text)

		cmd := strings.ToUpper(args[0])

		switch cmd {
		case "SET":
    		if len(args) != 4 {
				conn.Write([]byte("Err: wrong number of args for 'set'\n"))
				continue
			}

			key := args[1]
			val := args[2]
			expStr := args[3]

			
			expInt, err := strconv.Atoi(expStr)
			if err != nil {
				
				conn.Write([]byte("Err: 4th argument (TTL) must be an integer\n"))
				continue
			}

			
			duration := time.Duration(expInt) * time.Second
			c.Set(key, val, duration)
			
			conn.Write([]byte("OK\n"))

		case "GET":
			if len(args) != 2 {
				conn.Write([]byte("Err wrong number of args for 'get'\n"))
				continue
			}

			key := args[1]

			val, found := c.Get(key)

			if !found {
				conn.Write([]byte("(nil)\n"))
				
			} else {
				conn.Write([]byte(val + "\n"))
			}

		case "DEL":
			
			if len(args) != 2 {
				conn.Write([]byte("Err wrong number of args for 'del'\n"))
				continue

			}

			key := args[1]

			c.Delete(key)

			conn.Write([]byte("OK\n"))


		case "SAVE":
			err := c.SaveToFile("cache.dump")
			if err != nil {
				fmt.Println("Save error: ", err)
				conn.Write([]byte("ERR Internal error \n"))
			} else {
				conn.Write([]byte("OK\n"))
			}
		
		}


		
	}

	if err := scanner.Err(); err != nil {
		fmt.Println("Connection error: ", err)
	}


	fmt.Println("Client disconnected: ",conn.RemoteAddr().String())

	

	

	

	
}