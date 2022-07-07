package box

type Message struct {
	Type   string  `json:"type"`
	Action string  `json:"action"`
	Room   *Room   `json:"room"`
	Body   string  `json:"body"`
	Sender *Client `json:"sender"`
}
