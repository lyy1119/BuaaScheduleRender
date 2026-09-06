package web

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// TestWriteSchedulePageLogoutButton 验证"退出登录"按钮只在手动登录模式出现。
func TestWriteSchedulePageLogoutButton(t *testing.T) {
	// 手动模式：含退出登录
	rec := httptest.NewRecorder()
	writeSchedulePage(rec, "20261", true)
	if !strings.Contains(rec.Body.String(), "退出登录") || !strings.Contains(rec.Body.String(), "/logout") {
		t.Error("手动模式应显示退出登录按钮(/logout)")
	}
	// 自动登录模式：不含退出登录按钮
	rec2 := httptest.NewRecorder()
	writeSchedulePage(rec2, "20261", false)
	if strings.Contains(rec2.Body.String(), "退出登录") {
		t.Error("自动登录模式不应显示退出登录按钮")
	}
	// 两个页面都内嵌纯课表页 iframe 与打印链接
	for _, body := range []string{rec.Body.String(), rec2.Body.String()} {
		if !strings.Contains(body, "/schedule/print?sem=20261") {
			t.Error("课表页应包含纯课表 iframe/打印链接")
		}
	}
}

// TestWriteLoginPageError 验证登录失败提示会回显到登录页（不跳转）。
func TestWriteLoginPageError(t *testing.T) {
	rec := httptest.NewRecorder()
	writeLoginPage(rec, "登录失败：SSO 登录未成功（HTTP 401）：验证码")
	body := rec.Body.String()
	if !strings.Contains(body, "登录失败") || !strings.Contains(body, "验证码") {
		t.Error("登录页应显示失败原因")
	}
	if !strings.Contains(body, "form method=\"post\" action=\"/login\"") {
		t.Error("登录失败应停留登录表单页")
	}
}
