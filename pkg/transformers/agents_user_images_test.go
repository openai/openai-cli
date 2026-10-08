package transformers

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func agentsUserImageEvent(kind, id, content string) gjson.Result {
	return gjson.Parse(`{"type":"agent.session.turn.item.` + kind + `","event_id":"ev_user","session_id":"sess_test","turn_id":"turn_test","output_index":0,"future_event":9007199254740993,"item":{"id":` + id + `,"type":"message","turn_id":"turn_test","role":"user","status":"completed","phase":null,"future_item":null,"content":` + content + `}}`)
}

func TestAgentsUserImagesPreserveMixedContentAndLegacyIDs(t *testing.T) {
	encoded := strings.Repeat("QUJD", 65536)
	content := `[{"type":"input_text","text":"Read the first image."},{"type":"input_image","image_url":` + strconv.Quote("data:image/png;base64,"+encoded) + `,"future_image":9007199254740993},{"type":"input_text","text":"Then compare the next image."},{"type":"input_image","image_url":"data:image/webp;base64,QUJDRA=="},{"type":"future_input","image_url":"data:image/gif;base64,RlVUVVJF"},{"type":"input_text","text":"Keep the final line.\nSecond line."}]`
	for _, kind := range []string{"added", "done"} {
		for _, id := range []string{`"message_test"`, `null`} {
			t.Run(kind+"/"+id, func(t *testing.T) {
				raw := agentsUserImageEvent(kind, id, content).Raw
				for _, value := range []gjson.Result{gjson.Parse(raw), gjson.Get(`{"wrapper":`+raw+`}`, "wrapper")} {
					var projector AgentsStreamProjector
					event, projected, err := projector.Project(t.Context(), value, agentsEventsRoute)
					require.NoError(t, err)
					require.True(t, projected, "known user image data needs a human summary")
					require.True(t, projector.HasOmissions())
					for _, field := range []string{"type", "event_id", "session_id", "turn_id", "future_event", "item.id", "item.role", "item.status", "item.phase", "item.future_item", "item.content.0", "item.content.2", "item.content.4", "item.content.5", "item.content.1.future_image"} {
						require.Equal(t, value.Get(field).Raw, event.Details.Get(field).Raw, field)
					}
					require.Len(t, event.Details.Get("item.content").Array(), 6)
					require.Equal(t, fmt.Sprintf("(image/png; %d base64 characters)", len(encoded)), event.Details.Get("item.content.1.image_url").Str)
					require.Equal(t, "(image/webp; 8 base64 characters)", event.Details.Get("item.content.3.image_url").Str)
					if strings.Contains(event.Details.Raw, encoded) {
						t.Fatalf("human output retained %d encoded characters", len(encoded))
					}
					require.Equal(t, raw, value.Raw)
				}
			})
		}
	}
}

func TestAgentsUserImagesSupportGeneralDataURLs(t *testing.T) {
	for _, tc := range []struct{ url, label string }{
		{"data:image/jpeg;base64,QUJDRA==", "image/jpeg"},
		{"data:image/png;base64,QUJDRA==", "image/png"},
		{"data:image/gif;base64,QUJDRA==", "image/gif"},
		{"data:image/webp;base64,QUJDRA==", "image/webp"},
		{"data:image/svg+xml;charset=utf-8;base64,QUJDRA==", "image/svg+xml"},
		{"DATA:IMAGE/PNG;BASE64,QUJDRA==", "image/png"},
		{"data:;base64,QUJDRA==", "image data"},
		{"data:;charset=utf-8;base64,QUJDRA==", "image data"},
		{"data:image/png;base64,QUJDRA%3D%3D", "image/png"},
	} {
		t.Run(tc.url, func(t *testing.T) {
			value := agentsUserImageEvent("done", `null`, `[{"type":"input_image","image_url":`+strconv.Quote(tc.url)+`}]`)
			event, projected, err := new(AgentsStreamProjector).Project(t.Context(), value, agentsEventsRoute)
			require.NoError(t, err)
			require.True(t, projected)
			require.Equal(t, "("+tc.label+"; 8 base64 characters)", event.Details.Get("item.content.0.image_url").Str)
		})
	}
}

func TestAgentsUserImagesPreserveMalformedAndUnknownSlots(t *testing.T) {
	for _, content := range []string{
		`null`,
		`{"type":"input_image","image_url":"data:image/png;base64,QUJDRA=="}`,
		`[{"type":"input_image","image_url":null}]`,
		`[{"type":"input_image","image_url":42}]`,
		`[{"type":"input_image","image_url":"https://synthetic.invalid/image.png"}]`,
		`[{"type":"input_image","image_url":"data:image/png,unencoded"}]`,
		`[{"type":"input_image","image_url":"data:image/png;base64,invalid!"}]`,
		`[{"type":"input_image","image_url":"data:image/png;base64,"}]`,
		`[{"type":"input_image","image_url":"data:image/png;base64,QUJDRA%XX"}]`,
		`[{"type":"input_image","image_url":"data:image/no space;base64,QUJDRA=="}]`,
		`[{"type":"input_image","image_url":"data:not-a-mime-type;base64,QUJDRA=="}]`,
		`[{"type":"input_text","text":"data:image/png;base64,QUJDRA=="}]`,
		`[{"type":"future_image","image_url":"data:image/png;base64,QUJDRA=="}]`,
	} {
		t.Run(content, func(t *testing.T) {
			value := agentsUserImageEvent("added", `"message_test"`, content)
			var projector AgentsStreamProjector
			_, projected, err := projector.Project(t.Context(), value, agentsEventsRoute)
			require.NoError(t, err)
			require.False(t, projected)
			require.False(t, projector.HasOmissions())
			require.Equal(t, content, value.Get("item.content").Raw)
		})
	}
}

func TestAgentsUserImagesCancelDuringScan(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	controlled := &cancelSummaryContext{Context: ctx, cancel: cancel, after: 8}
	value := agentsUserImageEvent("done", `null`, `[{"type":"input_image","image_url":`+strconv.Quote("data:image/png;base64,"+strings.Repeat("QUJD", 65536))+`}]`)
	event, projected, err := new(AgentsStreamProjector).Project(controlled, value, agentsEventsRoute)
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, projected)
	require.False(t, event.Details.Exists())
}

func TestAgentsUserImagesKeepInvalidMediaBesideValidMedia(t *testing.T) {
	content := `[{"type":"input_image","image_url":"data:image/png;base64,invalid!"},{"type":"input_image","image_url":"data:image/png;base64,QUJDRA=="},{"type":"input_image","image_url":null},{"type":"input_image","image_url":"https://synthetic.invalid/image.png"}]`
	value := agentsUserImageEvent("done", `null`, content)
	event, projected, err := new(AgentsStreamProjector).Project(t.Context(), value, agentsEventsRoute)
	require.NoError(t, err)
	require.True(t, projected)
	for _, index := range []int{0, 2, 3} {
		path := fmt.Sprintf("item.content.%d", index)
		require.Equal(t, value.Get(path).Raw, event.Details.Get(path).Raw)
	}
	require.Equal(t, "(image/png; 8 base64 characters)", event.Details.Get("item.content.1.image_url").Str)
}
