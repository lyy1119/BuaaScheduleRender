package gsmis

import (
	"testing"
	"time"
)

// TestAutoSemester 验证学期自动推断的月份边界：
// 9月~12月 → (当年)1；1月 → (上一年)1；2月~8月 → (上一年)2。
func TestAutoSemester(t *testing.T) {
	cases := []struct {
		date string // YYYY-MM-DD
		want string
	}{
		{"2026-09-01", "20261"}, // 9 月起新学年第一学期
		{"2026-12-31", "20261"},
		{"2027-01-01", "20261"}, // 1 月仍属上学年第一学期
		{"2027-01-31", "20261"},
		{"2027-02-01", "20262"}, // 2 月起第二学期（上一学年 → 2026-2027-2）
		{"2027-06-15", "20262"},
		{"2027-08-31", "20262"},
		{"2027-09-01", "20271"}, // 新学年
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

// TestParseRawCookieHeader 验证 Cookie 头字符串解析。
func TestParseRawCookieHeader(t *testing.T) {
	cks := parseRawCookieHeader("GS_SESSIONID=abc; _WEU=xyz==; a=b; c")
	if len(cks) != 3 {
		t.Fatalf("解析出 %d 个 cookie, want 3", len(cks))
	}
	if cks[0].Name != "GS_SESSIONID" || cks[0].Value != "abc" {
		t.Errorf("cookie[0] = %v", cks[0])
	}
	if cks[1].Value != "xyz==" {
		t.Errorf("cookie[1] 值解析错误: %q", cks[1].Value)
	}
	if cks[2].Name != "a" || cks[2].Value != "b" {
		t.Errorf("cookie[2] = %v", cks[2])
	}
}
