package message

type messageAction string

var messageActionToString = map[messageAction]string{
	NotifyJoinRoomMessage: "notify-join-room-message",
	JoinRoomMessage:       "join-room-message",
	LeaveRoomMessage:      "leave-room-message",
	SendMessage:           "send-message",
	JoinRoom:              "join-room",
	LeaveRoom:             "leave-room",
}

func (a messageAction) String() string {
	if str, ok := messageActionToString[a]; ok {
		return str
	}
	return "Unknown MessageAction"
}

const (
	NotifyJoinRoomMessage messageAction = "notify-join-room-message"
	JoinRoomMessage       messageAction = "join-room-message"
	LeaveRoomMessage      messageAction = "leave-room-message"
	SendMessage           messageAction = "send-message"
	JoinRoom              messageAction = "join-room"
	LeaveRoom             messageAction = "leave-room"
)
