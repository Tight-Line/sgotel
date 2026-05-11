package sendgrid

import (
	"encoding/json"
	"fmt"
)

// Event is a single SendGrid event-webhook record.
//
// SendGrid posts a JSON array of these to the webhook endpoint. Field set is the
// union across event types; many fields are populated only for specific events.
type Event struct {
	Email       string     `json:"email"`
	Timestamp   int64      `json:"timestamp"`
	Event       string     `json:"event"`
	SGEventID   string     `json:"sg_event_id"`
	SGMessageID string     `json:"sg_message_id"`
	SMTPID      string     `json:"smtp-id"`
	Category    Categories `json:"category"`

	// Event-specific (populated as relevant).
	Reason    string `json:"reason,omitempty"`
	Status    string `json:"status,omitempty"`
	Type      string `json:"type,omitempty"`
	URL       string `json:"url,omitempty"`
	UserAgent string `json:"useragent,omitempty"`
	IP        string `json:"ip,omitempty"`
	Response  string `json:"response,omitempty"`
	Attempt   string `json:"attempt,omitempty"`

	// Custom args attached by the sender. SendGrid flattens these as top-level
	// keys on each event payload; we capture anything we don't recognize here.
	Custom map[string]any `json:"-"`
}

// knownFields is the set of top-level keys consumed by Event's typed fields.
// Anything outside this set is captured in Event.Custom.
var knownFields = map[string]struct{}{
	"email":         {},
	"timestamp":     {},
	"event":         {},
	"sg_event_id":   {},
	"sg_message_id": {},
	"smtp-id":       {},
	"category":      {},
	"reason":        {},
	"status":        {},
	"type":          {},
	"url":           {},
	"useragent":     {},
	"ip":            {},
	"response":      {},
	"attempt":       {},
}

func (e *Event) UnmarshalJSON(data []byte) error {
	type alias Event
	var a alias
	if err := json.Unmarshal(data, &a); err != nil {
		return err
	}
	*e = Event(a)

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	for k, v := range raw {
		if _, ok := knownFields[k]; ok {
			continue
		}
		var val any
		if err := json.Unmarshal(v, &val); err != nil {
			continue
		}
		if e.Custom == nil {
			e.Custom = make(map[string]any)
		}
		e.Custom[k] = val
	}
	return nil
}

// Categories accepts either a string or an array of strings, because SendGrid
// emits both depending on how categories were set at send time.
type Categories []string

func (c *Categories) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		*c = nil
		return nil
	}
	switch data[0] {
	case '"':
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		*c = []string{s}
		return nil
	case '[':
		var arr []string
		if err := json.Unmarshal(data, &arr); err != nil {
			return err
		}
		*c = arr
		return nil
	default:
		return fmt.Errorf("category: unexpected JSON token %q", data[0])
	}
}

// ParseBatch decodes a SendGrid webhook body (a JSON array of events).
func ParseBatch(body []byte) ([]Event, error) {
	var events []Event
	if err := json.Unmarshal(body, &events); err != nil {
		return nil, fmt.Errorf("parse batch: %w", err)
	}
	return events, nil
}
