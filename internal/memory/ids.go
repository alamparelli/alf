package memory

import (
	"crypto/rand"
	"fmt"
)

// NewConvID generates a new conversation ID. The format matches the legacy
// conversation.NewConvID so persisted IDs remain recognisable across the
// migration (see #336).
func NewConvID() ConvID {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return ConvID(fmt.Sprintf("conv-%x", b))
}

// NewMessageID generates a unique message ID. Format matches the legacy
// conversation.NewMessageID (UUID-like hex) so callers that persist or log
// these strings keep the same visual shape.
func NewMessageID() MsgID {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return MsgID(fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]))
}
