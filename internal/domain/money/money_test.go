package money

import "testing"

func TestDivRound(t *testing.T) {
	cases := []struct{ a, b, want int64 }{{5, 2, 3}, {4, 2, 2}, {-5, 2, -3}, {1, 3, 0}, {2, 3, 1}, {10, 0, 0}, {149, 100, 1}, {150, 100, 2}}
	for _, c := range cases {
		if got := DivRound(c.a, c.b); got != c.want {
			t.Errorf("DivRound(%d,%d)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestMulBpAndRatio(t *testing.T) {
	if got := MulBp(100_00, 8000); got != 80_00 {
		t.Fatalf("80%% of 100.00 = %d", got)
	}
	if got := MulBp(1, 5000); got != 1 { // 0.5 rounds away from zero
		t.Fatalf("got %d", got)
	}
	if Ratio(1, 0) != 0 || Ratio(1, 4) != 0.25 {
		t.Fatal("ratio")
	}
}

func TestFormatAndDisplay(t *testing.T) {
	if got := Format(123456789, "KES"); got != "KES 1,234,567.89" {
		t.Fatalf("got %q", got)
	}
	if got := Format(-5, "USD"); got != "-USD 0.05" {
		t.Fatalf("got %q", got)
	}
	if got := Display(8500000, "KES"); got != "KSh 85,000" {
		t.Fatalf("got %q", got)
	}
	if got := Display(8500049, "KES"); got != "KSh 85,000" {
		t.Fatalf("got %q", got)
	}
	if got := Display(125050, "USD"); got != "USD 1,250.50" {
		t.Fatalf("got %q", got)
	}
	if !ValidCurrency("KES") || ValidCurrency("kes") || ValidCurrency("KESH") {
		t.Fatal("currency validation")
	}
}
