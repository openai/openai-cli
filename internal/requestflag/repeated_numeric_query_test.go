package requestflag

import (
	"slices"
	"testing"
)

func TestRepeatedFractionalQueryFlag(t *testing.T) {
	flag := &Flag[[]float64]{Name: "weight", QueryPath: "weights"}
	if err := flag.PreParse(); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"1.5", "-2.25"} {
		if err := flag.Set("weight", value); err != nil {
			t.Fatal(err)
		}
	}
	got, ok := flag.Get().([]float64)
	if !ok || !slices.Equal(got, []float64{1.5, -2.25}) {
		t.Fatalf("fractional filters = %v, want [1.5 -2.25]", flag.Get())
	}
}
