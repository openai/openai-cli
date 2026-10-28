# Live SIP call controls

Use `live:sessions` to accept, transfer, reject, or end a SIP call. Set
`OPENAI_API_KEY` in your environment and use the session ID supplied for the
incoming call.

```sh
openai live:sessions accept --session-id "$SESSION_ID" \
  --session.type live \
  --session.model gpt-live-1 \
  --session.instructions 'Help the caller with their question.'
```

For a larger configuration, pipe JSON or YAML. `input` contains text history
supplied before the call starts; `instructions` controls the frontend voice agent.

```sh
cat <<'JSON' | openai live:sessions accept --session-id "$SESSION_ID"
{
  "session": {
    "type": "live",
    "model": "gpt-live-1",
    "instructions": "Help the caller with their question.",
    "input": [
      {
        "type": "message",
        "role": "user",
        "content": [{"type": "input_text", "text": "I need help with my order."}]
      }
    ]
  }
}
JSON
```

Transfer an accepted call to a SIP address or telephone number:

```sh
openai live:sessions refer --session-id "$SESSION_ID" \
  --target-uri 'sip:agent@example.com'
```

Reject an incoming call with an explicit SIP status, or end an active session:

```sh
openai live:sessions reject --session-id "$SESSION_ID" --status-code 486
openai live:sessions hangup --session-id "$SESSION_ID"
```

These commands send the GA request contract without an opt-in version header.
The CLI covers SIP call controls; use the Python or JavaScript SDK's Live helpers
for audio streaming, transcript grouping, and browser WebRTC connections.
