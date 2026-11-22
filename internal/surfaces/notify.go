package surfaces

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
)

// Notification is the payload a chat surface's Sender renders: an approval a
// person has to act on, or a reply the model wrote. Kind says which fields
// mean anything. It is the one shape every surface shares, so each sender only
// decides how to put it on its own wire.
type Notification struct {
	Kind      string `json:"kind"` // NotifyApproval | NotifyContent
	SessionID string `json:"session_id,omitempty"`
	ToolID    string `json:"tool_id,omitempty"`
	Text      string `json:"text,omitempty"`
}

// The two kinds of Notification.
const (
	NotifyApproval = "approval"
	NotifyContent  = "content"
)

// Render is the text a Sender puts in front of a person for a payload built by
// BuildNotification. A payload that is not a Notification is sent as it is.
func Render(payload []byte) string {
	var n Notification
	if err := json.Unmarshal(payload, &n); err != nil {
		return string(payload)
	}
	switch n.Kind {
	case NotifyApproval:
		return fmt.Sprintf("Approval needed for session %s (tool: %s) — review it in the run's own approval endpoint.", n.SessionID, n.ToolID)
	case NotifyContent:
		return n.Text
	}
	return string(payload)
}

// SenderFor builds the Sender that carries one tenant's messages over one
// surface: a sender needs that tenant's own credential, so it is built per
// delivery rather than held as one fixed instance.
type SenderFor func(tenantID uuid.UUID) Sender
