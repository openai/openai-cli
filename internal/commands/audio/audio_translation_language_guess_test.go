package audio

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

func TestTranslationLanguageGuess(t *testing.T) {
	for _, test := range []struct {
		caption, language string
	}{
		{"El tren sale a las nueve.", "Spanish"},
		{"El tren sale a las nueve. Vamos nos encontrar na entrada principal.", "Portuguese"},
		{"Vamos nos encontrar na entrada principal. J’ai oublié mon passeport à l’hôtel.", "French"},
		{"J’ai oublié mon passeport à l’hôtel. No te preocupes, todavía tenemos tiempo.", "Spanish"},
		{"Hello, how are you doing today? 今天我们一起去火车站吧。", "Chinese"},
		{"123 456 7890", ""},
		{"Bonjour", ""},
		{"abcde qwerty zxqv zzzzzz", ""},
		{"Alchen sale alasnowe verbose nouns", ""},
	} {
		require.Equal(t, test.language, guessTranslationLanguage(translationLanguageSample(test.caption)), test.caption)
	}
	sample := translationLanguageSample(strings.Repeat("日", 200))
	require.LessOrEqual(t, len(sample), 256)
	require.True(t, utf8.ValidString(sample))
	require.Empty(t, translationLanguageSample(strings.Repeat("\x80", 500)))
}

func TestTranslationLanguageGuessFollowsLatestCaptionAndSession(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	m := &terminalTranslationModel{ctx: ctx, width: 40, height: 16, started: true, finished: true, done: make(chan struct{})}
	m.state.Original = "El tren sale a las nueve."
	_, cmd := m.Update(terminalTranslationUpdateReady{session: m.done})
	require.NotNil(t, cmd)
	_, pending := m.Update(terminalTranslationUpdateReady{session: m.done})
	require.Nil(t, pending)

	m.state.Original += " Vamos nos encontrar na entrada principal."
	_, cmd = m.Update(cmd())
	require.NotNil(t, cmd)
	require.Empty(t, m.languageGuess)
	m.Update(cmd())
	require.Contains(t, m.View().Content, "Original · Likely Portuguese")
	_, cmd = m.Update(terminalTranslationUpdateReady{session: m.done})
	require.Nil(t, cmd)

	m.state.Original += " J’ai oublié mon passeport à l’hôtel."
	_, cmd = m.Update(terminalTranslationUpdateReady{session: m.done})
	require.NotNil(t, cmd)
	require.NoError(t, m.resetSession())
	m.done = make(chan struct{})
	m.started, m.finished = true, true
	m.Update(cmd())
	require.Empty(t, m.languageGuess)

	m.state.Original = "Bonjour"
	_, cmd = m.Update(terminalTranslationUpdateReady{session: m.done})
	require.NotNil(t, cmd)
	m.Update(cmd())
	require.Contains(t, m.View().Content, "Original · Language unsure")

	m.state.Original = "The next train leaves in ten minutes."
	_, cmd = m.Update(terminalTranslationUpdateReady{session: m.done})
	require.NotNil(t, cmd)
	cancel()
	require.Nil(t, cmd())
}
