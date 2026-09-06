package login

import (
	"testing"
)

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
