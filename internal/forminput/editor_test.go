package forminput

import "testing"

func TestEditorPreservesUnicodeSuffixWithSpareCapacity(t *testing.T) {
	text := make([]rune, 4, 30)
	copy(text, []rune("a猫b🙂"))
	e := Editor{Text: text, Cursor: 2}
	e.Insert("界x")
	if string(e.Text) != "a猫界xb🙂" || e.Cursor != 4 {
		t.Fatalf("%q cursor %d", string(e.Text), e.Cursor)
	}
	e.Backspace()
	e.Delete()
	if string(e.Text) != "a猫界🙂" || e.Cursor != 3 {
		t.Fatalf("%q %d", string(e.Text), e.Cursor)
	}
}
func TestEditorBoundariesAndPolicies(t *testing.T) {
	e := Editor{}
	e.Backspace()
	e.Delete()
	e.Move(-1)
	e.MoveRow(false)
	e.MoveRow(true)
	e.DeleteWordBefore()
	if len(e.Text) != 0 || e.Cursor != 0 {
		t.Fatal(e)
	}
	e.Insert("abc\nx\n猫dog")
	e.Cursor = 8
	e.LineStart()
	if e.Cursor != 6 {
		t.Fatal(e.Cursor)
	}
	e.LineEnd()
	if e.Cursor != 10 {
		t.Fatal(e.Cursor)
	}
	e.Cursor = 3
	e.MoveRow(true)
	if e.Cursor != 5 {
		t.Fatal(e.Cursor)
	}
	e.MoveRow(false)
	if e.Cursor != 1 {
		t.Fatal(e.Cursor)
	}
	e.Cursor = 4
	e.DeleteBefore()
	if string(e.Text) != "x\n猫dog" || e.Cursor != 0 {
		t.Fatal(e)
	}
	e.Clear()
	if e.Text != nil || e.Cursor != 0 {
		t.Fatal(e)
	}
}
func TestEditorDeleteWordPreservesFollowingText(t *testing.T) {
	e := Editor{Text: []rune("猫 dog  tail"), Cursor: 7}
	e.DeleteWordBefore()
	if string(e.Text) != "猫 tail" || e.Cursor != 2 {
		t.Fatalf("%q %d", string(e.Text), e.Cursor)
	}
}
func TestCycleSparseFocusAndMissingIDs(t *testing.T) {
	ids := []int{0, 7, 2, 19}
	for _, tc := range []struct {
		in   int
		back bool
		want int
	}{{0, false, 7}, {19, false, 0}, {0, true, 19}, {2, true, 7}} {
		got, ok := Cycle(ids, tc.in, tc.back)
		if !ok || got != tc.want {
			t.Fatal(tc, got, ok)
		}
	}
	if got, ok := Cycle(ids, 99, false); ok || got != 99 {
		t.Fatal(got, ok)
	}
	if got, ok := Cycle([]int(nil), 99, false); ok || got != 99 {
		t.Fatal(got, ok)
	}
}
