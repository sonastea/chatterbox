package websocket

import (
	"fmt"
	"log"

	"github.com/gorilla/websocket"
)

type Client struct {
	ID   string
	Conn *websocket.Conn
	Pool *Pool
}

type Message struct {
	Type int    `json:"type"`
	Body string `json:"body"`
}

func (c *Client) Read() {
	defer func() {
		c.Pool.Leave <- c
		c.Conn.Close()
	}()

	for {
		msgType, p, err := c.Conn.ReadMessage()
		if err != nil {
			log.Println(err)
			return
		}
		if p[0] == 47 {
			if (string(p[1:]) == "leave") {
				fmt.Printf("User has left the channel\n")
			}
		}
		message := Message{Type: msgType, Body: string(p)}
		c.Pool.Broadcast <- message
		fmt.Printf("Message received: %+v\n", message)
	}
}
