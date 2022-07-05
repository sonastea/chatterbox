package box

type Message struct {
	Type   string `json:"type"`
	Action string `json:"action"`
	Body   string `json:"body"`
	Sender string `json:"sender"`
}
