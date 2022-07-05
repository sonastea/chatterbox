package message

type messageAction string

func (a messageAction) String() string {
	switch a {
	case SendMessage:
		return "send-message"
	case JoinRoom:
		return "join-room"
	case LeaveRoom:
		return "leave-room"
	}
	return "Unknown Action"
}

const (
	SendMessage messageAction = "send-message"
	JoinRoom    messageAction = "join-room"
	LeaveRoom   messageAction = "leave-room"
)
