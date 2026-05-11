package sendgrid

import (
	"encoding/json"
	"testing"
)

func TestParseBatch_KnownFields(t *testing.T) {
	body := []byte(`[
		{
			"email": "alice@example.com",
			"timestamp": 1700000000,
			"event": "delivered",
			"sg_event_id": "evt-1",
			"sg_message_id": "msg-1",
			"smtp-id": "<smtp-1>",
			"category": "welcome",
			"response": "250 OK"
		}
	]`)
	events, err := ParseBatch(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("want 1 event, got %d", len(events))
	}
	e := events[0]
	if e.Email != "alice@example.com" {
		t.Errorf("email: %q", e.Email)
	}
	if e.Event != "delivered" {
		t.Errorf("event: %q", e.Event)
	}
	if e.SGEventID != "evt-1" {
		t.Errorf("sg_event_id: %q", e.SGEventID)
	}
	if e.SMTPID != "<smtp-1>" {
		t.Errorf("smtp-id: %q", e.SMTPID)
	}
	if len(e.Category) != 1 || e.Category[0] != "welcome" {
		t.Errorf("category: %v", e.Category)
	}
	if e.Response != "250 OK" {
		t.Errorf("response: %q", e.Response)
	}
}

func TestParseBatch_CategoryArray(t *testing.T) {
	body := []byte(`[{"event":"open","category":["a","b","c"]}]`)
	events, err := ParseBatch(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := events[0].Category; len(got) != 3 || got[0] != "a" || got[2] != "c" {
		t.Errorf("category: %v", got)
	}
}

func TestParseBatch_CategoryNull(t *testing.T) {
	body := []byte(`[{"event":"open","category":null}]`)
	events, err := ParseBatch(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if events[0].Category != nil {
		t.Errorf("want nil category, got %v", events[0].Category)
	}
}

func TestParseBatch_CustomArgs(t *testing.T) {
	body := []byte(`[{
		"event": "click",
		"email": "x@y.com",
		"url": "https://example.com",
		"user_id": 42,
		"campaign": "spring-sale",
		"tags": ["promo","active"]
	}]`)
	events, err := ParseBatch(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	c := events[0].Custom
	if got, ok := c["user_id"].(float64); !ok || got != 42 {
		t.Errorf("custom user_id: %v (%T)", c["user_id"], c["user_id"])
	}
	if got, ok := c["campaign"].(string); !ok || got != "spring-sale" {
		t.Errorf("custom campaign: %v", c["campaign"])
	}
	tags, ok := c["tags"].([]any)
	if !ok || len(tags) != 2 {
		t.Fatalf("custom tags: %v (%T)", c["tags"], c["tags"])
	}
	if _, dup := c["event"]; dup {
		t.Errorf("known field leaked into Custom: event")
	}
	if _, dup := c["url"]; dup {
		t.Errorf("known field leaked into Custom: url")
	}
}

func TestParseBatch_EventTypes(t *testing.T) {
	body := []byte(`[
		{"event":"bounce","type":"hard","reason":"550 unknown user","status":"5.1.1"},
		{"event":"click","url":"https://example.com","useragent":"Mozilla/5.0","ip":"1.2.3.4"},
		{"event":"deferred","attempt":"3","response":"421 try again"}
	]`)
	events, err := ParseBatch(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if events[0].Type != "hard" || events[0].Status != "5.1.1" {
		t.Errorf("bounce fields: %+v", events[0])
	}
	if events[1].URL != "https://example.com" || events[1].IP != "1.2.3.4" {
		t.Errorf("click fields: %+v", events[1])
	}
	if events[2].Attempt != "3" {
		t.Errorf("deferred attempt: %q", events[2].Attempt)
	}
}

func TestParseBatch_Malformed(t *testing.T) {
	_, err := ParseBatch([]byte(`{"not":"an array"}`))
	if err == nil {
		t.Fatal("want error on non-array body")
	}
}

func TestCategories_RoundtripMarshal(t *testing.T) {
	// Sanity: Categories should marshal back as a JSON array regardless of input form.
	for _, in := range []string{`"solo"`, `["a","b"]`} {
		var c Categories
		if err := json.Unmarshal([]byte(in), &c); err != nil {
			t.Fatalf("unmarshal %s: %v", in, err)
		}
		out, err := json.Marshal(c)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var rt Categories
		if err := json.Unmarshal(out, &rt); err != nil {
			t.Fatalf("roundtrip: %v", err)
		}
	}
}
