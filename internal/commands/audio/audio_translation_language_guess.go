package audio

import (
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	py3langid "github.com/markusmobius/go-py3langid"
	"golang.org/x/text/language"
	"golang.org/x/text/language/display"
)

type translationLanguageGuessMsg struct {
	session  <-chan struct{}
	language string
}

// Run at most one guess at a time for this session. Leave capture and playback
// workers independent of the detector, including its first model load.
func (m *terminalTranslationModel) queueTranslationLanguageGuess() tea.Cmd {
	if !m.started || m.languageGuessPending {
		return nil
	}
	sample := translationLanguageSample(m.state.Original)
	if sample == m.languageSample {
		return nil
	}
	m.languageSample = sample
	m.languageGuessPending = true
	ctx, session := m.ctx, m.done
	return func() tea.Msg {
		if ctx.Err() != nil {
			return nil
		}
		guess := guessTranslationLanguage(sample)
		if ctx.Err() != nil {
			return nil
		}
		return translationLanguageGuessMsg{session: session, language: guess}
	}
}

// Prefer the most recent sentence so earlier speech does not dominate a
// language switch. Limit only the detector's sample, not the saved captions.
func translationLanguageSample(caption string) string {
	caption = strings.TrimRightFunc(caption, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r)
	})
	if end := strings.LastIndexAny(caption, ".!?。！？…\n"); end >= 0 {
		_, width := utf8.DecodeRuneInString(caption[end:])
		caption = caption[end+width:]
	}
	const sampleBytes = 256
	if len(caption) > sampleBytes {
		start := len(caption) - sampleBytes
		for start < len(caption) && !utf8.RuneStart(caption[start]) {
			start++
		}
		caption = caption[start:]
	}
	return strings.TrimSpace(caption)
}

var translationLanguageDetector = sync.OnceValues(func() (*py3langid.Identifier, error) {
	return py3langid.NewDefaultIdentifier(py3langid.WithNormalizedProbabilities(), py3langid.WithMinConfidence(0.5))
})

func guessTranslationLanguage(sample string) string {
	letters := 0
	for _, r := range sample {
		if unicode.IsLetter(r) {
			letters++
		}
	}
	if letters < 10 {
		return ""
	}
	detector, err := translationLanguageDetector()
	if err != nil {
		return ""
	}
	guess, err := detector.IdentifyString(sample)
	if err != nil || guess.Language == "und" || guess.Language == "zxx" {
		return ""
	}
	tag, err := language.Parse(guess.Language)
	if err != nil {
		return ""
	}
	return display.English.Languages().Name(tag)
}
