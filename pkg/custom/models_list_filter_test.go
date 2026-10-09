package custom

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestModelsListFilterQuotedOperands(t *testing.T) {
	// Synthetic cases also cover the inspected SDK 588 operand matrix.
	for _, test := range []struct{ name, expression, want string }{
		{"bare", `id=review-a`, "review-a"},
		{"surrounding_space", "  id = review-a  ", "review-a"},
		{"tab_newline", "\t id\n=\t review-a\r\n", "review-a"},
		{"unicode_space", "\u00a0id\u2003=\u00a0review-a\u2003", "review-a"},
		{"double_quoted", `id="review-a"`, "review-a"},
		{"single_quoted", `id='review-a'`, "review-a"},
		{"interior_space", `id = " review a "`, " review a "},
		{"escaped_double", `id="review\"a"`, `review"a`},
		{"escaped_single", `id='review\'a'`, "review'a"},
		{"opposite_double", `id="review\'a"`, `review\'a`},
		{"opposite_single", `id='review\"a' `, `review\"a`},
		{"escaped_backslash", `id="review\\a"`, `review\a`},
		{"bare_backslash_pair", `id=review\\a`, `review\a`},
		{"unknown_escape", `id="review\qa"`, `review\qa`},
		{"newline_escape", `id="review\na"`, `review\na`},
		{"actual_newline", "id=\"review\na\"", "review\na"},
		{"embedded_operators", `id="a=b~c:d"`, "a=b~c:d"},
		{"bare_embedded_operators", `id=a=b~c:d`, "a=b~c:d"},
		{"empty_double", `id=""`, ""},
		{"empty_single", `id=''`, ""},
		{"empty_fragments", `id=""''`, ""},
		{"trailing_backslash", `id=review-a\`, `review-a\`},
		{"quoted_trailing_backslash", `id="review-a\\"`, `review-a\`},
		{"adjacent_quotes", `id="review-""a"`, "review-a"},
		{"mixed_quotes", `id=review-"a"`, "review-a"},
		{"trailing_fragment", `id="review-"a`, "review-a"},
		{"mixed_quote_types", `id=pre"quoted "'and 'post`, "prequoted and post"},
		{"escaped_space", `id=review\ a`, `review\ a`},
		{"escaped_leading_space", `id=\ review`, `\ review`},
		{"escaped_trailing_space", `id=review\ `, `review\ `},
		{"bare_escaped_double", `id=review\"a`, `review"a`},
		{"bare_escaped_single", `id=review\'a`, "review'a"},
		{"numeric_bare", `id=001`, "001"},
		{"numeric_quoted", `id="001"`, "001"},
		{"boolean_text", `id="a AND b OR NOT c"`, "a AND b OR NOT c"},
		{"boolean_word", `id=AND`, "AND"},
		// Models deliberately retain literal operators and bare parentheses.
		{"leading_equals", `id==a`, "=a"},
		{"leading_tilde", `id=~a`, "~a"},
		{"bare_parentheses", `id=a(b)`, "a(b)"},
		{"quoted_parentheses", `id="a(b)"`, "a(b)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			options, err := parseModelsListOptions(test.expression, true, "id")
			require.NoError(t, err)
			require.NotNil(t, options.selection.ExactID)
			require.Equal(t, test.want, *options.selection.ExactID)
			require.Nil(t, options.selection.Pattern)
		})
	}
}

func TestModelsListFilterRegexEscapesAndGroups(t *testing.T) {
	for _, test := range []struct{ expression, pattern, match, reject string }{
		{`id~"^review-\d+$"`, `^review-\d+$`, "review-123", "review-abc"},
		{`id ~ '^review-\w+$'`, `^review-\w+$`, "review-abc", "review-!"},
		{`id~"^review-\\\\a$"`, `^review-\\a$`, `review-\a`, "review-a"},
		{`id~^review-(a|b)$`, `^review-(a|b)$`, "review-a", "review-c"},
		{`id~"^review-(a|b)$"`, `^review-(a|b)$`, "review-b", "review-c"},
		{`id~(?i)^review$`, `(?i)^review$`, "REVIEW", "prefix-review"},
		{`id~\Qreview.a\E`, `\Qreview.a\E`, "review.a", "reviewxa"},
		{`id~"^review\na$"`, `^review\na$`, "review\na", `review\na`},
		{`id~~review`, `~review`, "~review", "review"},
		{`id~=review`, `=review`, "=review", "review"},
	} {
		t.Run(test.expression, func(t *testing.T) {
			options, err := parseModelsListOptions(test.expression, true, "~id")
			require.NoError(t, err)
			require.Equal(t, test.pattern, options.selection.Pattern.String())
			require.True(t, options.selection.Pattern.MatchString(test.match))
			require.False(t, options.selection.Pattern.MatchString(test.reject))
			require.Nil(t, options.selection.ExactID)
			require.True(t, options.selection.Descending)
		})
	}
}

func TestModelsListFilterWhitespaceAndBytePreservation(t *testing.T) {
	spaces := []rune{'\t', '\n', '\v', '\f', '\r', ' ', '\u001c', '\u001d', '\u001e', '\u001f',
		'\u0085', '\u00a0', '\u1680', '\u2000', '\u2001', '\u2002', '\u2003', '\u2004', '\u2005',
		'\u2006', '\u2007', '\u2008', '\u2009', '\u200a', '\u2028', '\u2029', '\u202f', '\u205f', '\u3000'}
	for _, space := range spaces {
		t.Run(fmt.Sprintf("U+%04X", space), func(t *testing.T) {
			padding := string(space)
			_, operand, err := parseModelsListFilter(padding + "id" + padding + "=" + padding + "review" + padding)
			require.NoError(t, err)
			require.Equal(t, "review", operand)
			_, operand, err = parseModelsListFilter(`id=left\` + padding + "right")
			require.NoError(t, err)
			require.Equal(t, "left\\"+padding+"right", operand)
			_, operand, err = parseModelsListFilter(`id="` + padding + "review" + padding + `"`)
			require.NoError(t, err)
			require.Equal(t, padding+"review"+padding, operand)
			_, _, err = parseModelsListFilter("id=left" + padding + "right")
			require.ErrorContains(t, err, "accepts one operand")
		})
	}
	for _, value := range []string{"\xff\xc3x", "\\\xff", "\\\x00", "\ufeffreview\u200b", "日本語e\u0301", "review\\"} {
		_, operand, err := parseModelsListFilter("id=" + value)
		require.NoError(t, err)
		require.Equal(t, []byte(value), []byte(operand))
	}
	allBytes := make([]byte, 256)
	for i := range allBytes {
		allBytes[i] = byte(i)
	}
	for _, quote := range []string{`"`, `'`} {
		escaped := strings.NewReplacer(`\`, `\\`, quote, `\`+quote).Replace(string(allBytes))
		_, operand, err := parseModelsListFilter("id=" + quote + escaped + quote)
		require.NoError(t, err)
		require.Equal(t, allBytes, []byte(operand))
	}
}

func TestModelsListFilterRejectsExtraSyntaxWithoutEcho(t *testing.T) {
	for _, test := range []struct{ expression, message string }{
		{"", "--filter accepts"},
		{"ID=synthetic-secret", "--filter accepts"},
		{"identifier=synthetic-secret", "--filter accepts"},
		{"id:synthetic-secret", "--filter accepts"},
		{"id", "--filter accepts"},
		{"id=", "requires an operand"},
		{"id~ \t\n", "requires an operand"},
		{`id="synthetic-secret`, "unterminated quote"},
		{`id='synthetic-secret`, "unterminated quote"},
		{`id="synthetic-secret\"`, "unterminated quote"},
		{`id="synthetic-secret" extra`, "accepts one operand"},
		{`id="synthetic-secret" id=other`, "accepts one operand"},
		{`id="synthetic-secret" OR id=other`, "accepts one operand"},
		{`id="synthetic-secret" "`, "accepts one operand"},
		{`id~"(?=synthetic-secret)"`, "invalid regular expression"},
	} {
		t.Run(test.expression, func(t *testing.T) {
			_, err := parseModelsListOptions(test.expression, true, "id")
			require.ErrorContains(t, err, test.message)
			require.NotContains(t, err.Error(), "synthetic-secret")
			var local *modelsListError
			require.ErrorAs(t, err, &local)
		})
	}
}

func TestModelsListFilterQuotedEmptySelection(t *testing.T) {
	for _, test := range []struct{ filter, want string }{
		{`id=""`, ""},
		{`id=''`, ""},
		{`id~""`, "review-a\nreview-b\n"},
		{`id~''`, "review-a\nreview-b\n"},
	} {
		source, _ := staticModelIterator(t, `[{"object":"model","id":"review-b"},{"object":"model","id":"review-a"}]`, -1, nil)
		options, err := parseModelsListOptions(test.filter, true, "id")
		require.NoError(t, err)
		var output bytes.Buffer
		opts := ShowJSONOpts{Context: context.WithValue(t.Context(), modelsListOptionsKey{}, options),
			Operation: "(resource) models > (method) list", OutputKind: OutputPageItem,
			Format: "jsonl", ExplicitFormat: true, Transform: "id", RawOutput: true, Stdout: &output}
		require.NoError(t, ShowJSONIterator(source, -1, opts))
		require.Equal(t, test.want, output.String())
		require.Equal(t, 2, source.Index())
	}
}

func TestModelsListFilterKeepsNumericIDsDistinct(t *testing.T) {
	for _, filter := range []string{`id=001`, `id="001"`} {
		source, _ := staticModelIterator(t, `[{"object":"model","id":"1"},{"object":"model","id":"001"}]`, -1, nil)
		options, err := parseModelsListOptions(filter, true, "id")
		require.NoError(t, err)
		var output bytes.Buffer
		opts := ShowJSONOpts{Context: context.WithValue(t.Context(), modelsListOptionsKey{}, options),
			Operation: "(resource) models > (method) list", OutputKind: OutputPageItem,
			Format: "jsonl", ExplicitFormat: true, Transform: "id", RawOutput: true, Stdout: &output}
		require.NoError(t, ShowJSONIterator(source, -1, opts))
		require.Equal(t, "001\n", output.String())
	}
}
