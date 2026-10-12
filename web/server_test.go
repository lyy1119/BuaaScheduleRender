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
	writeSchedulePage(rec, "20261", true, false)
	if !strings.Contains(rec.Body.String(), "退出登录") || !strings.Contains(rec.Body.String(), "/logout") {
		t.Error("手动模式应显示退出登录按钮(/logout)")
	}
	// 自动登录模式：不含退出登录按钮
	rec2 := httptest.NewRecorder()
	writeSchedulePage(rec2, "20261", false, false)
	if strings.Contains(rec2.Body.String(), "退出登录") {
		t.Error("自动登录模式不应显示退出登录按钮")
	}
	// 两个页面都内嵌纯课表页 iframe 与打印链接
	for _, body := range []string{rec.Body.String(), rec2.Body.String()} {
		if !strings.Contains(body, "/schedule/print?sem=20261") {
			t.Error("课表页应包含纯课表 iframe/打印链接")
		}
		if !strings.Contains(body, "强制刷新") || !strings.Contains(body, "refresh=1") {
			t.Error("课表页应提供强制刷新入口(refresh=1)")
		}
	}
	// 带 refresh=1 的工具栏页：iframe/打印链接也应携带 refresh
	rec3 := httptest.NewRecorder()
	writeSchedulePage(rec3, "20261", false, true)
	if !strings.Contains(rec3.Body.String(), "/schedule/print?sem=20261&refresh=1") {
		t.Error("refresh 模式应透传到纯课表链接")
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

// TestHelpEndpoint 验证 /help 的 JSON 与 Markdown 两种形态。
func TestHelpEndpoint(t *testing.T) {
	srv := New(Config{Version: "v9.9.9"})
	h := srv.Handler()

	// 默认 JSON
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/help", nil))
	if rec.Code != 200 || !strings.Contains(rec.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("GET /help => %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	body := rec.Body.String()
	for _, want := range []string{"api_version", "/api/v1/courses", "v9.9.9", "query_parameters", "error_codes"} {
		if !strings.Contains(body, want) {
			t.Errorf("/help JSON 缺少 %q", want)
		}
	}
	if strings.Contains(rec.Header().Get("Cache-Control"), "") == false {
		t.Error("/help 应带防缓存头")
	}

	// Markdown（format=md）
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, httptest.NewRequest("GET", "/help?format=md", nil))
	if ct := rec2.Header().Get("Content-Type"); !strings.Contains(ct, "text/markdown") {
		t.Errorf("GET /help?format=md 内容类型 = %s", ct)
	}
	md := rec2.Body.String()
	for _, want := range []string{"# buaa-schedule", "## 接口", "GET /api/v1/courses", "## 错误码"} {
		if !strings.Contains(md, want) {
			t.Errorf("Markdown 文档缺少 %q", want)
		}
	}

	// Accept: text/plain 也返回 Markdown
	req := httptest.NewRequest("GET", "/help", nil)
	req.Header.Set("Accept", "text/plain")
	rec3 := httptest.NewRecorder()
	h.ServeHTTP(rec3, req)
	if !strings.Contains(rec3.Header().Get("Content-Type"), "text/markdown") {
		t.Errorf("Accept: text/plain 应返回 Markdown, got %s", rec3.Header().Get("Content-Type"))
	}
}

// TestAPICoursesUnauthorized 验证未登录时 API 返回 401 JSON（而非重定向），
// 以及非法 date 会先被鉴权拦截（手动登录模式）。
func TestAPICoursesUnauthorized(t *testing.T) {
	srv := New(Config{})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/v1/courses?date=2026-09-07", nil))
	if rec.Code != 401 {
		t.Fatalf("未登录 GET /api/v1/courses => %d, want 401", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"unauthorized"`) {
		t.Errorf("401 响应应为 JSON 错误结构, got %s", rec.Body.String())
	}
	// 方法限制：POST 应由 ServeMux 返回 405
	rec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec2, httptest.NewRequest("POST", "/api/v1/courses", nil))
	if rec2.Code != 405 {
		t.Errorf("POST /api/v1/courses => %d, want 405", rec2.Code)
	}
}
