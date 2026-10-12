// Package login 提供北航统一身份认证（SSO, CAS）的会话与自动登录。
//
// 基本用法：
//
//	sess := login.NewSession()
//	attempt, err := sess.NewLoginAttempt(ctx, loginURL) // loginURL 形如 …/login?service=…
//	if attempt.CaptchaRequired() {                      // 触发验证码时
//	    png, mime, _ := attempt.CaptchaImage(ctx)       // 取验证码图片给用户
//	    …把用户输入的验证码传给 Submit
//	}
//	err = attempt.Submit(ctx, username, password, captcha)
//	// 若 err 为 ErrCaptchaRequired（验证码缺失/错误）→ attempt 已刷新出新验证码，可再次提交
//
// 登录成功后会话 Cookie 保存在 Session 的 CookieJar 中，可直接访问业务系统。
package login

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"mime"
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

// ErrCaptchaRequired 表示登录需要验证码（或上次提交的验证码不正确）。
// 出错时 LoginAttempt 已刷新出新的验证码图片，可重新取图并再次 Submit。
var ErrCaptchaRequired = errors.New("需要验证码")

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

// LoginAttempt 是一次 CAS 登录过程：绑定同一个 Session（Cookie）、登录页上的
// execution 令牌，以及可能的验证码 id。验证码与 execution 都是一次性的，
// 因此必须由同一个 Attempt 完成"取图 → 提交"。
type LoginAttempt struct {
	sess       *Session
	loginURL   string
	execution  string
	captchaID  string
	formAction string // 登录页 form 的 action（可为相对路径）
}

// NewLoginAttempt 拉取登录页并解析 execution 令牌与验证码信息。
func (s *Session) NewLoginAttempt(ctx context.Context, loginURL string) (*LoginAttempt, error) {
	a := &LoginAttempt{sess: s, loginURL: loginURL}
	if err := a.Refresh(ctx); err != nil {
		return nil, err
	}
	return a, nil
}

// Refresh 重新拉取登录页（刷新 execution 与验证码），失败返回错误。
// 用于"换一张验证码"或上次验证码输错后重新获取。
func (a *LoginAttempt) Refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.loginURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", DefaultUserAgent)
	resp, err := a.sess.HTTP.Do(req)
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
		return fmt.Errorf("SSO 登录页未找到 execution 令牌")
	}
	a.execution = string(m[1])
	a.captchaID = parseCaptchaID(page)
	if am := reFormAction.FindSubmatch(page); am != nil {
		a.formAction = string(am[1])
	} else {
		a.formAction = ""
	}
	return nil
}

// CaptchaRequired 报告本次登录是否需要验证码。
func (a *LoginAttempt) CaptchaRequired() bool { return a.captchaID != "" }

// CaptchaImage 下载当前验证码图片，返回图片字节与内容类型（通常 image/png）。
func (a *LoginAttempt) CaptchaImage(ctx context.Context) ([]byte, string, error) {
	if a.captchaID == "" {
		return nil, "", errors.New("当前登录不需要验证码")
	}
	u, err := url.Parse(a.loginURL)
	if err != nil {
		return nil, "", err
	}
	// 登录页中的相对地址 captcha?captchaId=… 解析到同源 /captcha
	imgURL := u.ResolveReference(&url.URL{
		Path:     "captcha",
		RawQuery: fmt.Sprintf("captchaId=%s&r=%d", url.QueryEscape(a.captchaID), rand.Intn(90000)+10000),
	}).String()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imgURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", DefaultUserAgent)
	req.Header.Set("Referer", a.loginURL)
	resp, err := a.sess.HTTP.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("获取验证码图片失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("获取验证码图片返回 HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, "", err
	}
	ctype := resp.Header.Get("Content-Type")
	if mt, _, err := mime.ParseMediaType(ctype); err == nil && mt != "" {
		ctype = mt // 去掉 charset 等参数，data URI 更规范
	} else if ctype == "" {
		ctype = "image/png"
	}
	return data, ctype, nil
}

// Submit 提交账号密码（需要验证码时同时提交 captcha）。
//
// 返回值：
//   - nil：登录成功（会话 Cookie 已写入 Jar）
//   - ErrCaptchaRequired：服务器要求验证码，或验证码不正确；此时 Attempt 已自动
//     刷新出新的验证码，可重新取图后再次 Submit
//   - 其它 error：账号密码错误、网络问题等
func (a *LoginAttempt) Submit(ctx context.Context, username, password, captcha string) error {
	// 提交地址：优先页面 form action，其次当前登录 URL（保留 service 参数）
	postURL := a.loginURL
	if a.formAction != "" {
		if strings.HasPrefix(a.formAction, "http") {
			postURL = a.formAction
		} else if strings.HasPrefix(a.formAction, "/") {
			if u, err := url.Parse(a.loginURL); err == nil {
				postURL = u.ResolveReference(&url.URL{Path: a.formAction}).String()
			}
		}
	}

	form := url.Values{
		"username":  {username},
		"password":  {password},
		"execution": {a.execution},
		"_eventId":  {"submit"},
		"type":      {"username_password"},
	}
	if captcha != "" {
		form.Set("captcha", captcha)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", DefaultUserAgent)
	req.Header.Set("Referer", a.loginURL)
	resp, err := a.sess.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("提交登录失败: %w", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	// 仍停留在登录页（页面里还有登录表单 / 验证码配置）说明没有登录成功
	if !looksLikeLoginPage(body) {
		return nil
	}

	pageErr := extractPageError(body)
	if captchaID := parseCaptchaID(body); captchaID != "" {
		// 服务器要求验证码（或验证码错误）：刷新出新的验证码供重试
		a.captchaID = captchaID
		if err := a.Refresh(ctx); err != nil {
			// 刷新失败也要告诉调用方需要验证码
			return fmt.Errorf("%w（刷新验证码失败: %v）", ErrCaptchaRequired, err)
		}
		if pageErr != "" {
			return fmt.Errorf("%w：%s", ErrCaptchaRequired, pageErr)
		}
		return ErrCaptchaRequired
	}
	if pageErr != "" {
		return errors.New(pageErr)
	}
	if hint := errorHint(body); hint != "" {
		return errors.New(strings.TrimPrefix(hint, "："))
	}
	return errors.New("登录失败：用户名或密码错误")
}

// CASLogin 是"一次性登录"的便捷封装：拉取登录页并提交账号密码。
// 若服务器要求验证码，返回的错误可用 errors.Is(err, ErrCaptchaRequired) 判断，
// 此时应改用 NewLoginAttempt + CaptchaImage + Submit 的流程。
func (s *Session) CASLogin(ctx context.Context, loginURL, username, password string) error {
	a, err := s.NewLoginAttempt(ctx, loginURL)
	if err != nil {
		return err
	}
	return a.Submit(ctx, username, password, "")
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

var (
	reExecution   = regexp.MustCompile(`name="execution"\s+value="([^"]+)"`)
	reFormAction  = regexp.MustCompile(`<form[^>]*action="([^"]*)"`)
	reCaptchaConf = regexp.MustCompile(`config\.captcha\s*=\s*\{([^}]*)\}`)
	reCaptchaID   = regexp.MustCompile(`id:\s*'([^']+)'`)
	rePageError   = regexp.MustCompile(`(?is)id="errorDiv"[^>]*>(.*?)</div>`)
	reTags        = regexp.MustCompile(`<[^>]*>`)
)

// parseCaptchaID 从登录页中解析验证码 id；不需要验证码时返回空串。
func parseCaptchaID(page []byte) string {
	m := reCaptchaConf.FindSubmatch(page)
	if m == nil {
		return ""
	}
	if id := reCaptchaID.FindSubmatch(m[1]); id != nil {
		return strings.TrimSpace(string(id[1]))
	}
	return ""
}

// looksLikeLoginPage 判断响应是否仍是登录页（含登录表单或验证码配置）。
func looksLikeLoginPage(body []byte) bool {
	s := string(body)
	return strings.Contains(s, `id="loginForm"`) || reCaptchaConf.Match(body)
}

// extractPageError 提取登录页中 #errorDiv 的提示文本（去掉 HTML 标签）。
func extractPageError(page []byte) string {
	m := rePageError.FindSubmatch(page)
	if m == nil {
		return ""
	}
	txt := reTags.ReplaceAllString(string(m[1]), " ")
	return strings.Join(strings.Fields(txt), " ")
}

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
