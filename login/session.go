// Package login 提供北航统一身份认证（SSO, CAS）的会话与自动登录。
//
// Session 持有一个带 CookieJar 的 HTTP 客户端；调用 CASLogin 完成
// sso.buaa.edu.cn 的 CAS 表单登录后，会话 Cookie 自动保存在 Jar 中，
// 供后续访问各业务系统（如 GSMIS 课表接口）使用。
package login

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// DefaultSSOLogin 统一身份认证登录页。
const DefaultSSOLogin = "https://sso.buaa.edu.cn/login"

// DefaultUserAgent 模拟浏览器标识。
const DefaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/152.0.0.0 Safari/537.36"

// Session 是一个带 CookieJar 的登录会话。
// 说明：内网站点证书常为自签，默认跳过证书校验（生产可按需改为正规信任链）。
type Session struct {
	// HTTP 是会话使用的 http.Client（Cookie 自动维护），可直接复用发请求。
	HTTP *http.Client
}

// NewSession 创建空会话（未登录）。
func NewSession() *Session {
	jar, _ := cookiejar.New(nil)
	return &Session{
		HTTP: &http.Client{
			Jar:       jar,
			Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
			Timeout:   60 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 12 {
					return fmt.Errorf("重定向次数过多")
				}
				return nil
			},
		},
	}
}

// AddRawCookieHeader 把浏览器/抓包得到的 Cookie 请求头字符串
// （形如 "GS_SESSIONID=...; _WEU=...; ..."）注入会话 CookieJar，
// 用于复用已登录会话；Cookie 会同时关联到参数 hosts 给出的域。
func (s *Session) AddRawCookieHeader(raw string, hosts ...string) error {
	cookies := parseRawCookieHeader(raw)
	if len(hosts) == 0 {
		if u, err := url.Parse(DefaultSSOLogin); err == nil {
			hosts = append(hosts, u.Scheme+"://"+u.Host)
		}
	}
	for _, host := range hosts {
		u, err := url.Parse(host)
		if err != nil {
			return err
		}
		s.HTTP.Jar.SetCookies(u, cookies)
	}
	return nil
}

// CookieHeader 导出会话当前持有的 Cookie 头（按 hosts 顺序收集去重）。
func (s *Session) CookieHeader(hosts ...string) string {
	var parts []string
	seen := map[string]bool{}
	for _, host := range hosts {
		u, err := url.Parse(host)
		if err != nil {
			continue
		}
		for _, ck := range s.HTTP.Jar.Cookies(u) {
			key := ck.Name + "=" + ck.Value
			if !seen[key] {
				seen[key] = true
				parts = append(parts, key)
			}
		}
	}
	return strings.Join(parts, "; ")
}

// CASLogin 在指定登录地址（形如 "https://sso.buaa.edu.cn/login?service=<回调>"）
// 上执行统一身份认证登录：
//
//	① GET 登录页（保存 SESSION Cookie），解析 execution 令牌；
//	② POST 登录表单（username/password/execution/_eventId/type）；
//	③ 跟随重定向（成功后带 ticket 跳回业务系统，业务系统会话 Cookie
//	   自动写入 Jar）。
func (s *Session) CASLogin(ctx context.Context, loginURL, username, password string) error {
	// ① 拉取登录页
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, loginURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", DefaultUserAgent)
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("打开 SSO 登录页失败: %w", err)
	}
	page, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		return err
	}
	m := reExecution.FindSubmatch(page)
	if m == nil {
		if reCaptcha.Match(page) {
			return fmt.Errorf("登录页要求验证码（或账号状态异常），无法自动登录，请手动登录一次后复用会话")
		}
		return fmt.Errorf("SSO 登录页未找到 execution 令牌")
	}
	execution := string(m[1])

	// ② 确定提交地址：优先页面 form action，其次当前登录 URL
	postURL := loginURL
	if am := reFormAction.FindSubmatch(page); am != nil {
		a := string(am[1])
		if strings.HasPrefix(a, "http") {
			postURL = a
		} else if strings.HasPrefix(a, "/") {
			if u, err := url.Parse(loginURL); err == nil {
				postURL = u.ResolveReference(&url.URL{Path: a}).String()
			}
		}
	}

	// ③ 提交表单并跟随跳转
	form := url.Values{
		"username":  {username},
		"password":  {password},
		"execution": {execution},
		"_eventId":  {"submit"},
		"type":      {"username_password"},
	}
	freq, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	freq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	freq.Header.Set("User-Agent", DefaultUserAgent)
	freq.Header.Set("Referer", loginURL)
	fresp, err := s.HTTP.Do(freq)
	if err != nil {
		return fmt.Errorf("提交登录失败: %w", err)
	}
	fbody, _ := io.ReadAll(fresp.Body)
	fresp.Body.Close()
	if hint := errorHint(fbody); hint != "" {
		return fmt.Errorf("SSO 登录未成功（HTTP %d）%s", fresp.StatusCode, hint)
	}
	return nil
}

var (
	reExecution  = regexp.MustCompile(`name="execution"\s+value="([^"]+)"`)
	reFormAction = regexp.MustCompile(`<form[^>]*action="([^"]*)"`)
	reCaptcha    = regexp.MustCompile(`(?i)captcha|验证码`)
)

// errorHint 从登录失败返回页面中摘取常见提示。
func errorHint(page []byte) string {
	for _, kw := range []string{"用户名或密码错误", "密码错误", "用户名不存在", "验证码", "账号已锁定", "locked"} {
		if strings.Contains(string(page), kw) {
			return "：" + kw
		}
	}
	return ""
}

func parseRawCookieHeader(raw string) []*http.Cookie {
	var out []*http.Cookie
	for _, seg := range strings.Split(raw, ";") {
		seg = strings.TrimSpace(seg)
		if seg == "" {
			continue
		}
		kv := strings.SplitN(seg, "=", 2)
		if len(kv) != 2 {
			continue
		}
		out = append(out, &http.Cookie{Name: strings.TrimSpace(kv[0]), Value: strings.TrimSpace(kv[1]), Path: "/"})
	}
	return out
}
