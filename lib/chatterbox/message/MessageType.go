package message

type messageType string

func (m messageType) String() string {
	switch m {
	case Normal, Broadcast, Command, Server:
		return string(m)
	default:
		return "Unknown MessageType"
	}
}

const (
	Normal    messageType = "normal"
	Broadcast messageType = "broadcast"
	Command   messageType = "command"
	Server    messageType = "server"
)
