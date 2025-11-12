package apiform

type Marshaler interface {
	MarshalMultipart() ([]byte, string, error)
}

type FormFormat int

const (
	// FormatRepeat represents arrays as repeated keys with the same value
	FormatRepeat FormFormat = iota
	// Comma-separated values 1,2,3
	FormatComma
	// FormatBrackets uses the key[] notation for arrays
	FormatBrackets
	// FormatIndicesDots uses key.0, key.1, etc. notation
	FormatIndicesDots
	// FormatIndicesBrackets uses key[0], key[1], etc. notation
	FormatIndicesBrackets
)
