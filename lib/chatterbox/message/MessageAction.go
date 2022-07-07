package message

type messageAction string

func (a messageAction) String() string {
	switch a {
	case NotifyJoinRoomMessage:
		return "notify-join-room-message"
	case JoinRoomMessage:
		return "join-room-message"
	case LeaveRoomMessage:
		return "leave-room-message"
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
	NotifyJoinRoomMessage messageAction = "notify-join-room-message"
	JoinRoomMessage       messageAction = "join-room-message"
	LeaveRoomMessage      messageAction = "leave-room-message"
	SendMessage           messageAction = "send-message"
	JoinRoom              messageAction = "join-room"
	LeaveRoom             messageAction = "leave-room"
)
