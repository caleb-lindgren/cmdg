package customize

import (
	"github.com/ThomasHabets/cmdg/pkg/input"
)

// Customizable interaction keys
type KeyOption struct {
	Key string
	Name string
}

var LeaveHelp = KeyOption {
	Key: input.Esc,
	Name: "ESC",
}

var ExitMessage = KeyOption {
	Key: input.Esc,
	Name: "ESC",
}

var ForwardMessage = KeyOption {
	Key: "w",
	Name: "w",
}

var ScrollDown = KeyOption {
	Key: "j",
	Name: "j",
}

var ScrollUp = KeyOption {
	Key: "k",
	Name: "k",
}

var PageDown = KeyOption {
	Key: "f",
	Name: "f",
}

var PageUp = KeyOption {
	Key: "b",
	Name: "b",
}

var DeleteMessage = KeyOption {
	Key: "d",
	Name: "d",
}

