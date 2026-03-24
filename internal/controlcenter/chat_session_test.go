package controlcenter

import "testing"

func TestConvSessionID(t *testing.T) {
	// Empty conv_id returns default apiChatID.
	if got := convSessionID(""); got != apiChatID {
		t.Errorf("convSessionID(\"\") = %d, want %d", got, apiChatID)
	}

	// Different conv_ids produce different session IDs.
	id1 := convSessionID("conv-aaa")
	id2 := convSessionID("conv-bbb")
	if id1 == id2 {
		t.Errorf("convSessionID produced same ID for different convs: %d", id1)
	}

	// Same conv_id is deterministic.
	if convSessionID("conv-aaa") != id1 {
		t.Error("convSessionID is not deterministic")
	}

	// All results must be negative (avoid Telegram ID collision).
	for _, cid := range []string{"a", "b", "conv-123", "00000000"} {
		if id := convSessionID(cid); id >= 0 {
			t.Errorf("convSessionID(%q) = %d, want negative", cid, id)
		}
	}

	// Must not collide with apiChatID (-1).
	for _, cid := range []string{"a", "test", "conv-1", "x"} {
		if id := convSessionID(cid); id == apiChatID {
			t.Errorf("convSessionID(%q) collides with apiChatID", cid)
		}
	}
}
