package box

import (
	"encoding/json"
	"log"

	"github.com/sonastea/chatterbox/internal/pkg/models"
)

type Message struct {
	Type   string      `json:"type"`
	Action string      `json:"action"`
	Room   *Room       `json:"room"`
	Body   string      `json:"body"`
	Sender models.User `json:"sender"`
}

func (message *Message) encode() []byte {
	json, err := json.Marshal(message)
	if err != nil {
		log.Println(err)
	}

	return json
}

func (message *Message) string() string {
	var msg string
	json.Unmarshal(message.encode(), &msg)

	return msg
}

func (message *Message) UnmarshalJSON(data []byte) error {
	type Alias Message
	msg := &struct {
		Sender Client `json:"sender"`
		*Alias
	}{
		Alias: (*Alias)(message),
	}
	if err := json.Unmarshal(data, &msg); err != nil {
		return err
	}
	message.Sender = &msg.Sender
	return nil
}
