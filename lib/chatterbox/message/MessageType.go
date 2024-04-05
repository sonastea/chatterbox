package message

type messageType string

var messageTypeToString = map[messageType]string{
    Normal:    "normal",
    Broadcast: "broadcast",
    Command:   "command",
    Server:    "server",
}

func (m messageType) String() string {
    if str, ok := messageTypeToString[m]; ok {
        return str
    }
    return "Unknown MessageType"
}

const (
	Normal    messageType = "normal"
	Broadcast messageType = "broadcast"
	Command   messageType = "command"
	Server    messageType = "server"
)
