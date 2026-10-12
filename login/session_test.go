package login

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
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

// fakeSSO 是一个模拟的统一身份认证服务器：
//   - GET  /login    返回带 execution 的登录页（needCaptcha 时附验证码配置）
//   - GET  /captcha  返回一张伪 PNG
//   - POST /login    验证码正确（或不需要验证码）时返回成功页，否则返回登录页
func fakeSSO(needCaptcha bool) *httptest.Server {
	page := func(withCaptcha bool) string {
		p := `<form id="loginForm" method="post" action="login">
  <input type="text" name="username">
  <input type="password" name="password">
  <input type="hidden" name="type" value="username_password">
  <input type="hidden" name="execution" value="EXEC-1">
</form>`
		if withCaptcha {
			p += "\n<script>config.captcha = { type: 'image',\n    id: '555' };</script>"
		}
		return p
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			_ = r.ParseForm()
			if needCaptcha && r.FormValue("captcha") != "1234" {
				_, _ = io.WriteString(w, page(true)) // 验证码错误：仍是登录页
				return
			}
			_, _ = io.WriteString(w, `<html><body>登录成功</body></html>`)
			return
		}
		_, _ = io.WriteString(w, page(needCaptcha))
	})
	mux.HandleFunc("/captcha", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("\x89PNG\r\n\x1a\nfakepng"))
	})
	return httptest.NewServer(mux)
}

// TestLoginAttemptCaptcha 验证"登录页要求验证码"的完整流程：
// 解析验证码 → 取图 → 不带验证码提交得到 ErrCaptchaRequired → 带正确验证码提交成功。
func TestLoginAttemptCaptcha(t *testing.T) {
	srv := fakeSSO(true)
	defer srv.Close()

	sess := NewSession()
	loginURL := srv.URL + "/login?service=demo"
	attempt, err := sess.NewLoginAttempt(context.Background(), loginURL)
	if err != nil {
		t.Fatal(err)
	}
	if !attempt.CaptchaRequired() {
		t.Fatal("应识别出登录页要求验证码")
	}
	img, mime, err := attempt.CaptchaImage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if mime != "image/png" || len(img) == 0 {
		t.Errorf("验证码图片 = %d 字节, mime=%s", len(img), mime)
	}
	// 不带验证码 → 需要验证码
	err = attempt.Submit(context.Background(), "user", "pass", "")
	if !errors.Is(err, ErrCaptchaRequired) {
		t.Fatalf("不带验证码提交应返回 ErrCaptchaRequired, got %v", err)
	}
	// attempt 应已自动刷新，仍可继续取图并提交
	if _, _, err := attempt.CaptchaImage(context.Background()); err != nil {
		t.Errorf("刷新后可再次取图: %v", err)
	}
	// 带正确验证码 → 成功
	if err := attempt.Submit(context.Background(), "user", "pass", "1234"); err != nil {
		t.Fatalf("带验证码提交应成功, got %v", err)
	}
}

// TestLoginAttemptWithoutCaptcha 验证不需要验证码时直接登录成功。
func TestLoginAttemptWithoutCaptcha(t *testing.T) {
	srv := fakeSSO(false)
	defer srv.Close()

	sess := NewSession()
	attempt, err := sess.NewLoginAttempt(context.Background(), srv.URL+"/login?service=demo")
	if err != nil {
		t.Fatal(err)
	}
	if attempt.CaptchaRequired() {
		t.Fatal("本场景不应要求验证码")
	}
	if err := sess.CASLogin(context.Background(), srv.URL+"/login?service=demo", "user", "pass"); err != nil {
		t.Fatalf("CASLogin 应成功, got %v", err)
	}
}
