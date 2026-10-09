package transformers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// ProjectCostRow contains an exact total for one project and currency.
// A nil project identifies unattributed costs. Currency spelling is preserved.
// Amount uses compact exponent notation when plain output requires excessive zeros.
type ProjectCostRow struct {
	ProjectID *string `json:"project_id"`
	Currency  string  `json:"currency"`
	Amount    string  `json:"amount"`
}

type projectCostKey struct {
	project  string
	assigned bool
	currency string
}

// ProjectCostAccumulator sums raw Costs responses without floating-point conversion.
// Discard the accumulator after an error. Rows also preserves that failure.
// Unknown fields do not contribute to this deliberately narrow projection.
type ProjectCostAccumulator struct {
	totals map[projectCostKey]costDecimal
	err    error
}

func NewProjectCostAccumulator() *ProjectCostAccumulator {
	return &ProjectCostAccumulator{totals: make(map[projectCostKey]costDecimal)}
}

// AddPage validates a complete page and returns its pagination fields.
// The caller follows cursors and rejects missing or repeated continuation cursors.
func (a *ProjectCostAccumulator) AddPage(ctx context.Context, raw []byte) (more bool, next string, err error) {
	defer func() {
		if err != nil {
			a.err = err
		}
	}()
	if a.err != nil {
		return false, "", a.err
	}
	var page struct {
		Data     []json.RawMessage `json:"data"`
		HasMore  *bool             `json:"has_more"`
		NextPage *string           `json:"next_page"`
	}
	if err = decodeCostObject(ctx, raw, &page, "data", "has_more", "next_page"); err != nil {
		return false, "", fmt.Errorf("invalid costs page: %w", err)
	}
	if page.Data == nil || page.HasMore == nil {
		return false, "", errors.New("costs page requires data and has_more")
	}
	if a.totals == nil {
		a.totals = make(map[projectCostKey]costDecimal)
	}
	for bucketIndex, rawBucket := range page.Data {
		var bucket struct {
			Results []json.RawMessage `json:"results"`
		}
		if err = decodeCostObject(ctx, rawBucket, &bucket, "results"); err != nil {
			return false, "", fmt.Errorf("invalid costs bucket %d: %w", bucketIndex, err)
		}
		if bucket.Results == nil {
			return false, "", fmt.Errorf("costs bucket %d requires results", bucketIndex)
		}
		for resultIndex, rawResult := range bucket.Results {
			key, amount, resultErr := projectCostResult(ctx, rawResult)
			if resultErr != nil {
				return false, "", fmt.Errorf("invalid cost in bucket %d, result %d: %w", bucketIndex, resultIndex, resultErr)
			}
			if previous, exists := a.totals[key]; exists {
				amount, err = previous.add(ctx, amount)
				if err != nil {
					return false, "", err
				}
			}
			a.totals[key] = amount
		}
	}
	if err = ctx.Err(); err != nil {
		return false, "", err
	}
	if page.NextPage != nil {
		next = *page.NextPage
	}
	return *page.HasMore, next, nil
}

func projectCostResult(ctx context.Context, raw []byte) (projectCostKey, costDecimal, error) {
	var result struct {
		Object    json.RawMessage `json:"object"`
		ProjectID *string         `json:"project_id"`
		Amount    json.RawMessage `json:"amount"`
	}
	var key projectCostKey
	if err := decodeCostObject(ctx, raw, &result, "object", "project_id", "amount"); err != nil {
		return key, costDecimal{}, err
	}
	if result.Object != nil && string(result.Object) != `"organization.costs.result"` {
		var object string
		if err := decodeCostJSON(ctx, result.Object, &object); err != nil || object != "organization.costs.result" {
			return key, costDecimal{}, errors.New("unsupported cost result object")
		}
	}
	if len(result.Amount) == 0 || bytes.Equal(result.Amount, []byte("null")) {
		return key, costDecimal{}, errors.New("cost requires an amount and a nonempty currency")
	}
	var money struct {
		Value    json.RawMessage `json:"value"`
		Currency string          `json:"currency"`
	}
	if err := decodeCostObject(ctx, result.Amount, &money, "value", "currency"); err != nil {
		return key, costDecimal{}, err
	}
	if money.Currency == "" {
		return key, costDecimal{}, errors.New("cost requires an amount and a nonempty currency")
	}
	value := money.Value
	if len(value) == 0 || (value[0] != '-' && (value[0] < '0' || value[0] > '9')) {
		return key, costDecimal{}, errors.New("cost amount requires a numeric value")
	}
	amount, err := parseCostDecimal(ctx, string(value))
	if err != nil {
		return key, costDecimal{}, err
	}
	key.currency = money.Currency
	if result.ProjectID != nil {
		key.assigned, key.project = true, *result.ProjectID
	}
	return key, amount, nil
}

// Rows returns null projects first, followed by project ID and currency order.
// Empty reports contain no rows and do not infer a currency.
func (a *ProjectCostAccumulator) Rows(ctx context.Context) ([]ProjectCostRow, error) {
	if a.err != nil {
		return nil, a.err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows := make([]ProjectCostRow, 0, len(a.totals))
	for key, value := range a.totals {
		amount, err := value.format(ctx)
		if err != nil {
			return nil, err
		}
		row := ProjectCostRow{Currency: key.currency, Amount: amount}
		if key.assigned {
			project := key.project
			row.ProjectID = &project
		}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		left, right := rows[i], rows[j]
		if left.ProjectID == nil || right.ProjectID == nil {
			if (left.ProjectID == nil) != (right.ProjectID == nil) {
				return left.ProjectID == nil
			}
		} else if *left.ProjectID != *right.ProjectID {
			return *left.ProjectID < *right.ProjectID
		}
		return left.Currency < right.Currency
	})
	return rows, ctx.Err()
}

func decodeCostJSON(ctx context.Context, raw []byte, target any) error {
	reader := &costJSONReader{ctx: ctx, source: bytes.NewReader(raw)}
	decoder := json.NewDecoder(reader)
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err != nil {
			return err
		}
		return errors.New("unexpected data after costs JSON")
	}
	return ctx.Err()
}

// Match encoding/json's case-insensitive field selection, including escaped keys.
// Unknown values remain raw JSON, so even enormous unknown numbers are ignored.
func decodeCostObject(ctx context.Context, raw []byte, target any, knownKeys ...string) error {
	decoder := json.NewDecoder(&costJSONReader{ctx: ctx, source: bytes.NewReader(raw)})
	decoder.UseNumber()
	opening, err := decoder.Token()
	if err != nil {
		return err
	}
	if opening != json.Delim('{') {
		return errors.New("costs data must be an object")
	}
	seen := make([]bool, len(knownKeys))
	for decoder.More() {
		if err := ctx.Err(); err != nil {
			return err
		}
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := token.(string)
		if !ok {
			return errors.New("costs object requires string keys")
		}
		for i, known := range knownKeys {
			if strings.EqualFold(key, known) {
				if seen[i] {
					return fmt.Errorf("duplicate costs field %q", known)
				}
				seen[i] = true
				break
			}
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return err
		}
	}
	if _, err := decoder.Token(); err != nil {
		return err
	}
	return decodeCostJSON(ctx, raw, target)
}

type costJSONReader struct {
	ctx    context.Context
	source *bytes.Reader
}

func (r *costJSONReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.source.Read(p[:min(len(p), 32*1024)])
}

// A decimal keeps a signed coefficient and a decimal scale, without float64.
// Arithmetic expands only digits required by the exact sum and polls cancellation.
// Raw coefficients are uncapped. Exponent expansion has a report-local bound.
type costDecimal struct {
	digits   string
	scale    int
	negative bool
}

const costMaxInt = int(^uint(0) >> 1)

// Bound only newly inserted digits, never coefficients supplied by the API.
const costMaxExpansion = 1 << 20

var errCostPrecision = errors.New("cost amount exceeds addressable decimal precision")

// ErrProjectCostExpansion identifies exact sums that exceed the report's expansion budget.
var ErrProjectCostExpansion = errors.New("exact cost addition requires more than 1048576 additional alignment digits")

func parseCostDecimal(ctx context.Context, raw string) (costDecimal, error) {
	value := costDecimal{negative: strings.HasPrefix(raw, "-")}
	if value.negative {
		raw = raw[1:]
	}
	var exponentText string
	if exponentIndex := strings.IndexAny(raw, "eE"); exponentIndex >= 0 {
		exponentText = raw[exponentIndex+1:]
		raw = raw[:exponentIndex]
	}
	var digits strings.Builder
	for i := 0; i < len(raw); i++ {
		if i%4096 == 0 {
			if err := ctx.Err(); err != nil {
				return value, err
			}
		}
		if raw[i] == '.' {
			value.scale = len(raw) - i - 1
		} else {
			digits.WriteByte(raw[i])
		}
	}
	value.digits = digits.String()
	value, err := value.normalize(ctx)
	if err != nil || value.digits == "0" || exponentText == "" {
		return value, err
	}
	exponent, err := strconv.Atoi(exponentText)
	if err != nil || exponent == -costMaxInt-1 {
		return value, errCostPrecision
	}
	if exponent > 0 && value.scale < -costMaxInt+exponent || exponent < 0 && value.scale > costMaxInt+exponent {
		return value, errCostPrecision
	}
	value.scale -= exponent
	return value, ctx.Err()
}

func (v costDecimal) normalize(ctx context.Context) (costDecimal, error) {
	first, last := 0, len(v.digits)
	for first < last && v.digits[first] == '0' {
		if first%4096 == 0 {
			if err := ctx.Err(); err != nil {
				return v, err
			}
		}
		first++
	}
	if first == last {
		return costDecimal{digits: "0"}, ctx.Err()
	}
	for last > first && v.digits[last-1] == '0' {
		if last%4096 == 0 {
			if err := ctx.Err(); err != nil {
				return v, err
			}
		}
		last--
	}
	trimmed := len(v.digits) - last
	if v.scale < -costMaxInt+trimmed {
		return v, errCostPrecision
	}
	v.scale -= trimmed
	v.digits = v.digits[first:last]
	return v, ctx.Err()
}

func (v costDecimal) span(scale int) (int, int, error) {
	if v.scale < 0 && scale > costMaxInt+v.scale {
		return 0, 0, errCostPrecision
	}
	shift := scale - v.scale
	if len(v.digits) > costMaxInt-shift {
		return 0, 0, errCostPrecision
	}
	return len(v.digits) + shift, shift, nil
}

func (v costDecimal) add(ctx context.Context, other costDecimal) (costDecimal, error) {
	if err := ctx.Err(); err != nil {
		return v, err
	}
	if v.digits == "0" {
		return other, nil
	}
	if other.digits == "0" {
		return v, nil
	}
	scale := max(v.scale, other.scale)
	leftSize, leftShift, err := v.span(scale)
	if err != nil {
		return v, err
	}
	rightSize, rightShift, err := other.span(scale)
	if err != nil {
		return v, err
	}
	if max(leftSize, rightSize)-max(len(v.digits), len(other.digits)) > costMaxExpansion {
		return v, ErrProjectCostExpansion
	}
	subtract := v.negative != other.negative
	if subtract {
		comparison := leftSize - rightSize
		if comparison == 0 {
			comparison = strings.Compare(v.digits, other.digits)
		}
		if comparison == 0 {
			return costDecimal{digits: "0"}, nil
		}
		if comparison < 0 {
			v, other = other, v
			leftShift, rightShift = rightShift, leftShift
		}
	}
	var reversed []byte
	carry := 0
	for i := 0; i < max(leftSize, rightSize); i++ {
		if i%4096 == 0 {
			if err := ctx.Err(); err != nil {
				return v, err
			}
		}
		left, right := v.digit(i-leftShift), other.digit(i-rightShift)
		if subtract {
			digit := left - right - carry
			carry = 0
			if digit < 0 {
				digit, carry = digit+10, 1
			}
			reversed = append(reversed, byte('0'+digit))
		} else {
			digit := left + right + carry
			reversed = append(reversed, byte('0'+digit%10))
			carry = digit / 10
		}
	}
	if carry != 0 {
		if len(reversed) == costMaxInt {
			return v, errCostPrecision
		}
		reversed = append(reversed, byte('0'+carry))
	}
	for i, j := 0, len(reversed)-1; i < j; i, j = i+1, j-1 {
		if i%4096 == 0 {
			if err := ctx.Err(); err != nil {
				return v, err
			}
		}
		reversed[i], reversed[j] = reversed[j], reversed[i]
	}
	return (costDecimal{digits: string(reversed), scale: scale, negative: v.negative}).normalize(ctx)
}

func (v costDecimal) digit(position int) int {
	if position < 0 || position >= len(v.digits) {
		return 0
	}
	return int(v.digits[len(v.digits)-1-position] - '0')
}

func (v costDecimal) format(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var out strings.Builder
	if v.negative {
		out.WriteByte('-')
	}
	inserted := 0
	if v.scale <= 0 {
		inserted = -v.scale
	} else if v.scale >= len(v.digits) {
		inserted = v.scale - len(v.digits)
	}
	if inserted > costMaxExpansion {
		out.WriteString(v.digits)
		out.WriteByte('e')
		out.WriteString(strconv.Itoa(-v.scale))
		return out.String(), ctx.Err()
	}
	zeroes := 0
	if v.scale <= 0 {
		if v.scale < -costMaxInt+len(v.digits)+out.Len() {
			return "", errCostPrecision
		}
		out.WriteString(v.digits)
		zeroes = -v.scale
	} else if v.scale < len(v.digits) {
		point := len(v.digits) - v.scale
		out.WriteString(v.digits[:point])
		out.WriteByte('.')
		out.WriteString(v.digits[point:])
	} else {
		if v.scale > costMaxInt-2-out.Len() {
			return "", errCostPrecision
		}
		out.WriteString("0.")
		zeroes = v.scale - len(v.digits)
	}
	const zeroChunk = "0000000000000000000000000000000000000000000000000000000000000000"
	for zeroes > 0 {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		n := min(zeroes, len(zeroChunk))
		out.WriteString(zeroChunk[:n])
		zeroes -= n
	}
	if v.scale >= len(v.digits) {
		out.WriteString(v.digits)
	}
	return out.String(), ctx.Err()
}
