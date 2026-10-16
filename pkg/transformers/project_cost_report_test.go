package transformers

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"math/rand"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func costTestPage(results string) []byte {
	return []byte(`{"data":[{"results":[` + results + `]}],"has_more":false,"next_page":null}`)
}

func costTestResult(project, currency, amount string) string {
	return `{"object":"organization.costs.result","project_id":` + project + `,"amount":{"currency":` + strconv.Quote(currency) + `,"value":` + amount + `}}`
}

func TestProjectCostAccumulatorExactTotalsAndStableRows(t *testing.T) {
	accumulator := NewProjectCostAccumulator()
	first := `{"data":[{"results":[` + strings.Join([]string{
		costTestResult(`"proj_b"`, "usd", "9007199254740993.00000000000000000001"),
		costTestResult(`"proj_a"`, "usd", "0.1"),
		costTestResult(`null`, "eur", "1.2300e2"),
		costTestResult(`"proj_a"`, "USD", "-2.0"),
	}, ",") + `]}],"has_more":true,"next_page":"next-synthetic-page"}`
	more, next, err := accumulator.AddPage(t.Context(), []byte(first))
	require.NoError(t, err)
	require.True(t, more)
	require.Equal(t, "next-synthetic-page", next)
	more, next, err = accumulator.AddPage(t.Context(), costTestPage(strings.Join([]string{
		costTestResult(`"proj_b"`, "usd", "0.00999999999999999999"),
		costTestResult(`"proj_a"`, "usd", "2e-1"),
		`{"object":"organization.costs.result","amount":{"currency":"eur","value":-0.5}}`,
		costTestResult(`""`, "usd", "-0e20"),
	}, ",")))
	require.NoError(t, err)
	require.False(t, more)
	require.Empty(t, next)
	rows, err := accumulator.Rows(t.Context())
	require.NoError(t, err)
	encoded, err := json.Marshal(rows)
	require.NoError(t, err)
	require.Equal(t, `[{"project_id":null,"currency":"eur","amount":"122.5"},{"project_id":"","currency":"usd","amount":"0"},{"project_id":"proj_a","currency":"USD","amount":"-2"},{"project_id":"proj_a","currency":"usd","amount":"0.3"},{"project_id":"proj_b","currency":"usd","amount":"9007199254740993.01"}]`, string(encoded))
}

func TestProjectCostAccumulatorDecimalArithmetic(t *testing.T) {
	for _, tc := range []struct {
		name, left, right, want string
	}{
		{"fraction carry", "0.999", "0.001", "1"},
		{"integer carry", "99999999999999999999", "1", "100000000000000000000"},
		{"credit", "123.4", "-23.456", "99.944"},
		{"negative total", "23.456", "-123.4", "-99.944"},
		{"negative addition", "-0.12", "-0.008", "-0.128"},
		{"equal cancellation", "-123000e-3", "123.000", "0"},
		{"prefix subtraction", "1.2", "-1.2000000000000000001", "-0.0000000000000000001"},
		{"positive exponent", "1.234e3", "6e1", "1294"},
		{"negative exponent", "12e-8", "1e-8", "0.00000013"},
		{"zero", "-0.000000", "0e-30", "0"},
		{"zero with huge exponent", "-0e999999999999999999999999999", "0e-999999999999999999999999999", "0"},
		{"large exponent addition", "1e1000000000", "2e1000000000", "3e1000000000"},
		{"large exponent carry", "9e1000000000", "9e1000000000", "18e1000000000"},
		{"large exponent credits", "1e1000000000", "-2e1000000000", "-1e1000000000"},
		{"large exponent cancellation", "1e1000000000", "-1e1000000000", "0"},
		{"small exponent addition", "1e-1000000000", "2e-1000000000", "3e-1000000000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			accumulator := NewProjectCostAccumulator()
			_, _, err := accumulator.AddPage(t.Context(), costTestPage(costTestResult(`null`, "usd", tc.left)+","+costTestResult(`null`, "usd", tc.right)))
			require.NoError(t, err)
			rows, err := accumulator.Rows(t.Context())
			require.NoError(t, err)
			require.Equal(t, tc.want, rows[0].Amount)
		})
	}
}

func TestProjectCostAccumulatorMatchesRationalOracle(t *testing.T) {
	random := rand.New(rand.NewSource(20261009))
	for trial := 0; trial < 200; trial++ {
		accumulator := NewProjectCostAccumulator()
		want := new(big.Rat)
		for term := 0; term < 12; term++ {
			coefficient := strconv.FormatInt(random.Int63n(2000000000000)-1000000000000, 10)
			if term%3 == 0 && coefficient != "0" {
				coefficient += strings.Repeat("0", random.Intn(7))
			}
			input := fmt.Sprintf("%se%d", coefficient, random.Intn(25)-12)
			rational, ok := new(big.Rat).SetString(input)
			require.True(t, ok)
			want.Add(want, rational)
			_, _, err := accumulator.AddPage(t.Context(), costTestPage(costTestResult(`null`, "usd", input)))
			require.NoError(t, err)
		}
		rows, err := accumulator.Rows(t.Context())
		require.NoError(t, err)
		require.Len(t, rows, 1)
		got, ok := new(big.Rat).SetString(rows[0].Amount)
		require.True(t, ok)
		require.Zero(t, want.Cmp(got), "trial %d: amount %s differs from %s", trial, rows[0].Amount, want.RatString())
	}
}

func TestProjectCostAccumulatorUnknownFieldsAndEmptyPages(t *testing.T) {
	accumulator := NewProjectCostAccumulator()
	_, _, err := accumulator.AddPage(t.Context(), []byte(`{"object":"page","data":[],"has_more":false,"next_page":null,"future":{"number":9007199254740993123456789,"value":[null,true]}}`))
	require.NoError(t, err)
	rows, err := accumulator.Rows(t.Context())
	require.NoError(t, err)
	require.NotNil(t, rows)
	require.Empty(t, rows)
	_, _, err = accumulator.AddPage(t.Context(), []byte(`{"future":1e999999999999999999999999999,"future":2,"DATA":[{"future_bucket":[1,2],"future_bucket":null,"ReSuLtS":[{"object":"organization.\u0063osts.result","future_result":true,"future_result":false,"project_id":null,"AMOUNT":{"CuRrEnCy":"usd","v\u0061lue":1,"future_amount":false,"future_amount":true}}]},{"results":[]}],"HAS_MORE":false}`))
	require.NoError(t, err)
	rows, err = accumulator.Rows(t.Context())
	require.NoError(t, err)
	require.Equal(t, []ProjectCostRow{{Currency: "usd", Amount: "1"}}, rows)
}

func TestProjectCostAccumulatorRejectsDuplicateKnownKeys(t *testing.T) {
	result := costTestResult(`null`, "usd", "1")
	page := string(costTestPage(result))
	for _, tc := range []struct{ name, input string }{
		{"page data", strings.Replace(page, `"data":`, `"data":[],"data":`, 1)},
		{"page has_more", strings.Replace(page, `"has_more":false`, `"has_more":true,"has_more":false`, 1)},
		{"page next_page", strings.Replace(page, `"next_page":null`, `"next_page":"hidden","next_page":null`, 1)},
		{"bucket results", strings.Replace(page, `"results":`, `"results":[],"results":`, 1)},
		{"result object", strings.Replace(page, `"object":`, `"object":"organization.usage.future.result","object":`, 1)},
		{"result project", strings.Replace(page, `"project_id":`, `"project_id":"proj_hidden","project_id":`, 1)},
		{"result amount", strings.Replace(page, `"amount":`, `"amount":{"value":1000,"currency":"usd"},"amount":`, 1)},
		{"money value", strings.Replace(page, `"value":1`, `"value":1000,"value":1`, 1)},
		{"money currency", strings.Replace(page, `"currency":"usd"`, `"currency":"eur","currency":"usd"`, 1)},
		{"case-insensitive page", strings.Replace(page, `"data":`, `"DATA":[],"data":`, 1)},
		{"escaped page", strings.Replace(page, `"data":`, `"d\u0061ta":[],"data":`, 1)},
		{"case-insensitive money", strings.Replace(page, `"value":1`, `"VALUE":1000,"value":1`, 1)},
		{"escaped money", strings.Replace(page, `"value":1`, `"v\u0061lue":1000,"value":1`, 1)},
		{"unicode case fold", strings.Replace(page, `"results":`, `"re\u017fults":[],"results":`, 1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			accumulator := NewProjectCostAccumulator()
			_, _, err := accumulator.AddPage(t.Context(), []byte(tc.input))
			require.ErrorContains(t, err, "duplicate costs field")
			rows, rowsErr := accumulator.Rows(t.Context())
			require.Nil(t, rows)
			require.ErrorIs(t, rowsErr, err)
		})
	}
}

func TestProjectCostAccumulatorRejectsIncompletePages(t *testing.T) {
	for _, input := range []string{
		`null`, `[]`, `{}`, `{"data":[],"has_more":null}`, `{"has_more":false}`,
		`{"data":null,"has_more":false}`, `{"data":{},"has_more":false}`,
		`{"data":[],"has_more":0}`, `{"data":[],"has_more":"false"}`,
		`{"data":[],"has_more":false,"next_page":1}`,
		`{"data":[null],"has_more":false}`, `{"data":[{}],"has_more":false}`,
		`{"data":[{"results":null}],"has_more":false}`,
		`{"data":[{"results":{}}],"has_more":false}`,
		`{"data":[],"has_more":false} {}`, `{"data":[],"has_more":false`,
	} {
		t.Run(input, func(t *testing.T) {
			accumulator := NewProjectCostAccumulator()
			_, _, err := accumulator.AddPage(t.Context(), []byte(input))
			require.Error(t, err)
			rows, rowsErr := accumulator.Rows(t.Context())
			require.Nil(t, rows)
			require.ErrorIs(t, rowsErr, err)
		})
	}
}

func TestProjectCostAccumulatorRejectsIncompleteMoneyAndUnknownObjects(t *testing.T) {
	for _, input := range []string{
		`null`, `[]`, `{}`, `{"object":"organization.costs.result","amount":null}`,
		`{"object":"organization.costs.result","amount":{"value":1}}`,
		`{"object":"organization.costs.result","amount":{"currency":"","value":1}}`,
		`{"object":"organization.costs.result","amount":{"currency":null,"value":1}}`,
		`{"object":"organization.costs.result","amount":{"currency":123,"value":1}}`,
		`{"object":"organization.costs.result","amount":{"currency":"usd"}}`,
		`{"object":"organization.costs.result","amount":{"currency":"usd","value":null}}`,
		`{"object":"organization.costs.result","amount":{"currency":"usd","value":"1.25"}}`,
		`{"object":"organization.costs.result","amount":{"currency":"usd","value":true}}`,
		`{"object":"organization.costs.result","amount":{"currency":"usd","value":[]}}`,
		`{"object":"organization.costs.result","amount":{"currency":"usd","value":{}}}`,
		`{"object":"organization.costs.result","project_id":12,"amount":{"currency":"usd","value":1}}`,
		`{"object":null,"amount":{"currency":"usd","value":1}}`,
		`{"object":"organization.usage.future.result","amount":{"currency":"usd","value":1}}`,
	} {
		t.Run(input, func(t *testing.T) {
			accumulator := NewProjectCostAccumulator()
			_, _, err := accumulator.AddPage(t.Context(), costTestPage(costTestResult(`"proj_a"`, "usd", "2")+","+input))
			require.Error(t, err)
			rows, rowsErr := accumulator.Rows(t.Context())
			require.Nil(t, rows, "never expose a partial total after malformed data")
			require.ErrorIs(t, rowsErr, err)
			_, _, nextErr := accumulator.AddPage(t.Context(), costTestPage(costTestResult(`"proj_a"`, "usd", "3")))
			require.ErrorIs(t, nextErr, err)
		})
	}
}

func TestProjectCostAccumulatorRequiresResultObject(t *testing.T) {
	accumulator := NewProjectCostAccumulator()
	missingObject := `{"project_id":"proj_a","amount":{"currency":"usd","value":3}}`
	_, _, err := accumulator.AddPage(t.Context(), costTestPage(costTestResult(`"proj_a"`, "usd", "2")+","+missingObject))
	require.ErrorContains(t, err, "cost result requires object")
	rows, rowsErr := accumulator.Rows(t.Context())
	require.Nil(t, rows)
	require.ErrorIs(t, rowsErr, err)
}

func TestProjectCostAccumulatorLargeExactCoefficient(t *testing.T) {
	accumulator := NewProjectCostAccumulator()
	large := strings.Repeat("9", 256*1024)
	_, _, err := accumulator.AddPage(t.Context(), costTestPage(costTestResult(`null`, "usd", large)+","+costTestResult(`null`, "usd", "1")))
	require.NoError(t, err)
	rows, err := accumulator.Rows(t.Context())
	require.NoError(t, err)
	require.Equal(t, "1"+strings.Repeat("0", len(large)), rows[0].Amount)

	// The expansion budget does not cap digits already supplied by the API.
	accumulator = NewProjectCostAccumulator()
	large = strings.Repeat("1", costMaxExpansion+1)
	_, _, err = accumulator.AddPage(t.Context(), costTestPage(costTestResult(`null`, "usd", large)+","+costTestResult(`null`, "usd", "1")))
	require.NoError(t, err)
	rows, err = accumulator.Rows(t.Context())
	require.NoError(t, err)
	require.Equal(t, large[:len(large)-1]+"2", rows[0].Amount)
}

func TestProjectCostAccumulatorCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	accumulator := NewProjectCostAccumulator()
	_, _, err := accumulator.AddPage(ctx, costTestPage(costTestResult(`null`, "usd", "1")))
	require.ErrorIs(t, err, context.Canceled)
	rows, err := NewProjectCostAccumulator().Rows(ctx)
	require.Nil(t, rows)
	require.ErrorIs(t, err, context.Canceled)

	t.Run("while reading a large unknown field", func(t *testing.T) {
		ctx := costTestCancelAfter(t, 5)
		input := []byte(`{"unknown":"` + strings.Repeat("x", 1024*1024) + `","data":[],"has_more":false}`)
		_, _, err := NewProjectCostAccumulator().AddPage(ctx, input)
		require.ErrorIs(t, err, context.Canceled)
	})
	t.Run("during exact addition", func(t *testing.T) {
		left := costDecimal{digits: strings.Repeat("9", 128*1024)}
		_, err := left.add(costTestCancelAfter(t, 3), costDecimal{digits: "1"})
		require.ErrorIs(t, err, context.Canceled)
	})
	t.Run("during exponent expansion", func(t *testing.T) {
		value := costDecimal{digits: "1", scale: -costMaxExpansion}
		_, err := value.format(costTestCancelAfter(t, 4))
		require.ErrorIs(t, err, context.Canceled)
	})
}

func TestProjectCostAccumulatorCancellationDuringSorting(t *testing.T) {
	const count = 1024
	accumulator := NewProjectCostAccumulator()
	for i := count - 1; i >= 0; i-- {
		key := projectCostKey{project: fmt.Sprintf("proj_%04d", i), assigned: true, currency: "usd"}
		accumulator.totals[key] = costDecimal{digits: "1"}
	}
	// Rows checks once, then each one-digit amount checks twice before sorting.
	// Trigger cancellation only after the sorter has completed several comparisons.
	ctx := costTestCancelAfter(t, 1+2*count+32)
	var rows []ProjectCostRow
	var err error
	require.NotPanics(t, func() { rows, err = accumulator.Rows(ctx) })
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, rows, "discard the partially sorted projection")

	rows, err = accumulator.Rows(t.Context())
	require.NoError(t, err)
	require.Len(t, rows, count)
	for i, row := range rows {
		require.Equal(t, fmt.Sprintf("proj_%04d", i), *row.ProjectID)
		require.Equal(t, "1", row.Amount)
	}
}

func TestProjectCostAccumulatorSortingPropagatesOtherPanics(t *testing.T) {
	accumulator := NewProjectCostAccumulator()
	accumulator.totals[projectCostKey{currency: "usd"}] = costDecimal{digits: "1"}
	accumulator.totals[projectCostKey{currency: "eur"}] = costDecimal{digits: "1"}
	checks := 0
	ctx := costTestErrContext{Context: t.Context(), err: func() error {
		checks++
		if checks == 6 {
			panic("unrelated sort panic")
		}
		return nil
	}}
	require.PanicsWithValue(t, "unrelated sort panic", func() { _, _ = accumulator.Rows(ctx) })
}

type costTestErrContext struct {
	context.Context
	err func() error
}

func (ctx costTestErrContext) Err() error { return ctx.err() }

func TestProjectCostAccumulatorUnrepresentableExponent(t *testing.T) {
	for _, value := range []string{"1e999999999999999999999999999", "1e-999999999999999999999999999"} {
		accumulator := NewProjectCostAccumulator()
		_, _, err := accumulator.AddPage(t.Context(), costTestPage(costTestResult(`null`, "usd", value)))
		if err == nil {
			_, err = accumulator.Rows(t.Context())
		}
		require.ErrorIs(t, err, errCostPrecision)
	}
}

func TestProjectCostAccumulatorOutputExpansionBoundary(t *testing.T) {
	for _, inserted := range []int{costMaxExpansion - 1, costMaxExpansion, costMaxExpansion + 1} {
		for _, negativeScale := range []bool{false, true} {
			t.Run(fmt.Sprintf("inserted=%d/negative_scale=%t", inserted, negativeScale), func(t *testing.T) {
				scale := inserted + 1
				if negativeScale {
					scale = -inserted
				}
				input := "1e" + strconv.Itoa(-scale)
				accumulator := NewProjectCostAccumulator()
				_, _, err := accumulator.AddPage(t.Context(), costTestPage(costTestResult(`null`, "usd", input)))
				require.NoError(t, err)
				rows, err := accumulator.Rows(t.Context())
				require.NoError(t, err)
				if inserted > costMaxExpansion {
					require.Equal(t, input, rows[0].Amount)
				} else if negativeScale {
					require.Equal(t, "1"+strings.Repeat("0", inserted), rows[0].Amount)
				} else {
					require.Equal(t, "0."+strings.Repeat("0", inserted)+"1", rows[0].Amount)
				}
			})
		}
	}
	for _, input := range []string{"1e309", "1e1000000000", "-1e-1000000000", fmt.Sprintf("1e%d", costMaxInt)} {
		accumulator := NewProjectCostAccumulator()
		_, _, err := accumulator.AddPage(t.Context(), costTestPage(costTestResult(`null`, "usd", input)))
		require.NoError(t, err)
		rows, err := accumulator.Rows(t.Context())
		require.NoError(t, err)
		if input == "1e309" {
			require.Equal(t, "1"+strings.Repeat("0", 309), rows[0].Amount)
		} else {
			require.Equal(t, input, rows[0].Amount)
		}
	}
}

func TestProjectCostAccumulatorAdditionExpansionBoundary(t *testing.T) {
	for _, inserted := range []int{costMaxExpansion - 1, costMaxExpansion, costMaxExpansion + 1, 1000000000} {
		t.Run(fmt.Sprintf("inserted=%d", inserted), func(t *testing.T) {
			accumulator := NewProjectCostAccumulator()
			input := "1e" + strconv.Itoa(inserted)
			_, _, err := accumulator.AddPage(t.Context(), costTestPage(costTestResult(`null`, "usd", input)+","+costTestResult(`null`, "usd", "1")))
			if inserted > costMaxExpansion {
				require.ErrorIs(t, err, ErrProjectCostExpansion)
				rows, rowsErr := accumulator.Rows(t.Context())
				require.Nil(t, rows)
				require.ErrorIs(t, rowsErr, ErrProjectCostExpansion)
				return
			}
			require.NoError(t, err)
			rows, err := accumulator.Rows(t.Context())
			require.NoError(t, err)
			require.Equal(t, "1"+strings.Repeat("0", inserted-1)+"1", rows[0].Amount)
		})
	}
}

func TestProjectCostAccumulatorExpansionBudgetPersistsAcrossPages(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values []string
		failAt int
	}{
		{"repeated expansion", []string{"1", "1e1048576", "1e2097152", "1e3145728"}, 2},
		{"credits and zero", []string{"1", "1e1048576", "-1", "0", "1e2097152", "1e3145728"}, 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			accumulator := NewProjectCostAccumulator()
			for index, value := range tc.values {
				_, _, err := accumulator.AddPage(t.Context(), costTestPage(costTestResult(`null`, "usd", value)))
				if index < tc.failAt {
					require.NoError(t, err, "page %d", index+1)
				} else {
					require.ErrorIs(t, err, ErrProjectCostExpansion, "page %d", index+1)
				}
			}
			rows, err := accumulator.Rows(t.Context())
			require.Nil(t, rows)
			require.ErrorIs(t, err, ErrProjectCostExpansion)
		})
	}
}

func TestProjectCostAccumulatorRetainsOriginalCoefficientAfterCancellation(t *testing.T) {
	accumulator := NewProjectCostAccumulator()
	// The five original coefficient digits survive normalization, credits, and zero.
	for _, value := range []string{"100.00", "-100.00", "0", "1", fmt.Sprintf("1e%d", costMaxExpansion+4)} {
		_, _, err := accumulator.AddPage(t.Context(), costTestPage(costTestResult(`null`, "usd", value)))
		require.NoError(t, err)
	}
	rows, err := accumulator.Rows(t.Context())
	require.NoError(t, err)
	require.Equal(t, "1"+strings.Repeat("0", costMaxExpansion+3)+"1", rows[0].Amount)
}

func TestProjectCostAccumulatorCarryRespectsExpansionBudget(t *testing.T) {
	for _, exponent := range []int{costMaxExpansion - 1, costMaxExpansion} {
		t.Run(fmt.Sprintf("exponent=%d", exponent), func(t *testing.T) {
			accumulator := NewProjectCostAccumulator()
			large := "9e" + strconv.Itoa(exponent)
			for _, value := range []string{large, "9"} {
				_, _, err := accumulator.AddPage(t.Context(), costTestPage(costTestResult(`null`, "usd", value)))
				require.NoError(t, err)
			}
			_, _, err := accumulator.AddPage(t.Context(), costTestPage(costTestResult(`null`, "usd", large)))
			if exponent == costMaxExpansion {
				require.ErrorIs(t, err, ErrProjectCostExpansion)
				rows, rowsErr := accumulator.Rows(t.Context())
				require.Nil(t, rows)
				require.ErrorIs(t, rowsErr, ErrProjectCostExpansion)
				return
			}
			require.NoError(t, err)
			rows, err := accumulator.Rows(t.Context())
			require.NoError(t, err)
			require.Equal(t, "18"+strings.Repeat("0", exponent-1)+"9", rows[0].Amount)
		})
	}
}

// Trigger real context cancellation at a deterministic polling boundary.
type costTestCancellationContext struct {
	context.Context
	cancel    context.CancelFunc
	remaining int
}

func costTestCancelAfter(t *testing.T, calls int) context.Context {
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	return &costTestCancellationContext{Context: ctx, cancel: cancel, remaining: calls}
}

func (ctx *costTestCancellationContext) Err() error {
	ctx.remaining--
	if ctx.remaining == 0 {
		ctx.cancel()
	}
	return ctx.Context.Err()
}
