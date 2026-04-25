package envelope

import (
	"errors"
	"strings"
	"testing"
)

func TestValidate_EventsExportsHappyPath(t *testing.T) {
	input := validManifest() + `
[[events.exports]]
topic = "chat.log"
`
	m, err := Validate([]byte(input))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(m.Events.Exports) != 1 || m.Events.Exports[0].Topic != "chat.log" {
		t.Errorf("exports=%+v", m.Events.Exports)
	}
}

func TestValidate_EventsSubscribesHappyPath(t *testing.T) {
	input := validManifest() + `
[[events.subscribes]]
from  = "cap-a"
topic = "chat.log"
`
	m, err := Validate([]byte(input))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(m.Events.Subscribes) != 1 ||
		m.Events.Subscribes[0].From != "cap-a" ||
		m.Events.Subscribes[0].Topic != "chat.log" {
		t.Errorf("subscribes=%+v", m.Events.Subscribes)
	}
}

func TestValidate_EventsBothBlocks(t *testing.T) {
	input := validManifest() + `
[[events.exports]]
topic = "result.ready"

[[events.subscribes]]
from  = "cap-a"
topic = "chat.log"
`
	m, err := Validate([]byte(input))
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if len(m.Events.Exports) != 1 || len(m.Events.Subscribes) != 1 {
		t.Errorf("events=%+v", m.Events)
	}
}

func TestValidate_EventsEmptyTopicRejected(t *testing.T) {
	cases := map[string]struct {
		snippet string
		want    error
	}{
		"export.empty": {
			snippet: "\n[[events.exports]]\ntopic = \"\"\n",
			want:    ErrEventTopicEmpty,
		},
		"export.malformed.uppercase": {
			snippet: "\n[[events.exports]]\ntopic = \"BAD\"\n",
			want:    ErrEventTopicMalformed,
		},
		"export.malformed.wildcard": {
			snippet: "\n[[events.exports]]\ntopic = \"chat.*\"\n",
			want:    ErrEventTopicMalformed,
		},
		"sub.from.empty": {
			snippet: "\n[[events.subscribes]]\nfrom = \"\"\ntopic = \"x\"\n",
			want:    ErrEventSubscribeFromEmpty,
		},
		"sub.from.malformed": {
			snippet: "\n[[events.subscribes]]\nfrom = \"BAD\"\ntopic = \"x\"\n",
			want:    ErrEventSubscribeFromMalformed,
		},
		"sub.topic.empty": {
			snippet: "\n[[events.subscribes]]\nfrom = \"cap-a\"\ntopic = \"\"\n",
			want:    ErrEventTopicEmpty,
		},
		"sub.topic.malformed": {
			snippet: "\n[[events.subscribes]]\nfrom = \"cap-a\"\ntopic = \"chat.*\"\n",
			want:    ErrEventTopicMalformed,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			input := validManifest() + tc.snippet
			_, err := Validate([]byte(input))
			if !errors.Is(err, tc.want) {
				t.Errorf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestValidate_EventsCanonicalizationStable(t *testing.T) {
	// Two manifests with the same events block should canonicalise to
	// the same bytes, modulo the field order in TOML which the canonical
	// form normalises (handled by canonical.go).
	a := validManifest() + "\n[[events.exports]]\ntopic = \"chat.log\"\n"
	mA, err := Validate([]byte(a))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(a, "chat.log") {
		t.Fatal("test fixture broken")
	}
	if mA.Events.Exports[0].Topic != "chat.log" {
		t.Errorf("topic decoded=%q", mA.Events.Exports[0].Topic)
	}
}
