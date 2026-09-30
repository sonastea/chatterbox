package message

type messageAction string

func (a messageAction) String() string {
	switch a {
	case NotifyJoinRoomMessage, JoinRoomMessage, LeaveRoomMessage, SendMessage, JoinRoom, LeaveRoom:
		return string(a)
	default:
		return "Unknown MessageAction"
	}
}

const (
	NotifyJoinRoomMessage messageAction = "notify-join-room-message"
	JoinRoomMessage       messageAction = "join-room-message"
	LeaveRoomMessage      messageAction = "leave-room-message"
	SendMessage           messageAction = "send-message"
	JoinRoom              messageAction = "join-room"
	LeaveRoom             messageAction = "leave-room"
)
