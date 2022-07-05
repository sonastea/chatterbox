package message

type messageType string

func (m messageType) String() string {
	switch m {
	case Normal:
		return "normal"
	case Command:
		return "command"
	case Server:
		return "server"
	}
	return "Unknown MessageType"
}

const (
	Normal  messageType = "normal"
	Command messageType = "command"
	Server  messageType = "server"
)
