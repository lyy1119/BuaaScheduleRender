package fetch

import (
	"testing"
	"time"
)

// TestAutoSemester 验证学期自动推断的月份边界：
// 9月~12月 → (当年)1；1月 → (上一年)1；2月~8月 → (上一年)2。
func TestAutoSemester(t *testing.T) {
	cases := []struct {
		date string
		want string
	}{
		{"2026-09-01", "20261"},
		{"2026-12-31", "20261"},
		{"2027-01-01", "20261"},
		{"2027-01-31", "20261"},
		{"2027-02-01", "20262"},
		{"2027-06-15", "20262"},
		{"2027-08-31", "20262"},
		{"2027-09-01", "20271"},
	}
	for _, c := range cases {
		now, err := time.ParseInLocation("2006-01-02", c.date, time.Local)
		if err != nil {
			t.Fatal(err)
		}
		if got := AutoSemester(now); got != c.want {
			t.Errorf("AutoSemester(%s) = %s, want %s", c.date, got, c.want)
		}
	}
}
