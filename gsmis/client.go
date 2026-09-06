// Package gsmis 提供北航研究生教务（GSMIS）课表数据的自动获取：
//
//  1. 登录：访问 gsmis 触发跳转 → 在 sso.buaa.edu.cn 完成 CAS 登录
//     （账号/密码 + execution 表单提交）→ 会话 Cookie（GS_SESSIONID 等）
//     由 http.Client 的 CookieJar 自动保存，之后无需手动携带 Cookie；
//  2. 取数：POST loadXskbData.do，表单字段 XNXQDM=<5 位学期号>
//     （如 20261 = 2026-2027 学年第一学期），返回与 testdata/loadXskbData.json
//     同构的 JSON；
//  3. 学期推断：默认按当前时间给出学期号（9 月~次年 1 月 → xxxx1；
//     次年 2 月~8 月 → 上学期号+1 的 xxxx2），也保留手动指定学期接口。
//
// 返回的 JSON 可由根包 schedule.ParseXskb 继续解析为课表结构。
package gsmis

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

// 默认服务地址（同一内网/校网环境可访问；需要时可覆盖）。
const (
	DefaultGsmisBase = "https://gsmis.buaa.edu.cn"
	DefaultIndexPath = "/gsapp/sys/wdkbapp/*default/index.do"
	DefaultDataPath  = "/gsapp/sys/wdkbapp/bykb/loadXskbData.do"
	DefaultSSOLogin  = "https://sso.buaa.edu.cn/login"
	DefaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/152.0.0.0 Safari/537.36"
)

// Client 持有一个带 CookieJar 的 HTTP 客户端，可自动完成登录并爬取课表。
type Client struct {
	username, password string
	gsmisBase          string // 如 https://gsmis.buaa.edu.cn
	httpc              *http.Client
}

// NewClient 构造自动登录客户端。username/password 为统一身份认证账号。
// 说明：内网教务站点证书可能为自签，此处按内网环境跳过证书校验；
// 生产使用建议改为系统信任链（把 transport 参数化）。
func NewClient(username, password string) *Client {
	jar, _ := cookiejar.New(nil)
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	return &Client{
		username:  username,
		password:  password,
		gsmisBase: DefaultGsmisBase,
		httpc: &http.Client{
			Jar:       jar,
			Transport: tr,
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

// SetBase 覆盖 gsmis 服务地址（默认 DefaultGsmisBase）。
func (c *Client) SetBase(base string) { c.gsmisBase = base }

// SetInsecureVerify 设置是否校验 HTTPS 证书（默认关闭校验）。
func (c *Client) SetInsecureVerify(verify bool) {
	if tr, ok := c.httpc.Transport.(*http.Transport); ok {
		tr.TLSClientConfig.InsecureSkipVerify = !verify
	}
}

// AutoSemester 根据当前时间推断默认学期号：
//
//	9 月 ~ 12 月：本学年第一学期 → 20261
//	1 月：          上一学年第一学期 → 20251
//	2 月 ~ 8 月：    上一学年第二学期 → 20252
//
// 目前一年按两个学期处理（第三学期可后续扩展）。
func AutoSemester(now time.Time) string {
	y := now.Year()
	switch m := now.Month(); {
	case m >= 9:
		return fmt.Sprintf("%d1", y)
	case m == 1:
		return fmt.Sprintf("%d1", y-1)
	default: // 2 月 ~ 8 月
		return fmt.Sprintf("%d2", y-1)
	}
}

var (
	reExecution  = regexp.MustCompile(`name="execution"\s+value="([^"]+)"`)
	rePassword   = regexp.MustCompile(`name="password"[^>]*`)
	reCaptcha    = regexp.MustCompile(`(?i)captcha|验证码`)
	reFormAction = regexp.MustCompile(`<form[^>]*action="([^"]*)"`)
)

// FetchJSON 返回指定学期课表 JSON 字节。
// 若当前会话未登录/已过期则自动完成 SSO 登录后重试一次。
// semester 为 5 位学期号（如 "20261"）；传空串表示按 AutoSemester(time.Now())。
func (c *Client) FetchJSON(ctx context.Context, semester string) ([]byte, error) {
	if semester == "" {
		semester = AutoSemester(time.Now())
	}
	body, err := c.postData(ctx, semester)
	if err != nil {
		return nil, err
	}
	if !looksLoggedIn(body) {
		// 会话失效（401/登录页 HTML）→ 登录后重试一次
		if err := c.doLogin(ctx); err != nil {
			return nil, err
		}
		body, err = c.postData(ctx, semester)
		if err != nil {
			return nil, err
		}
		if !looksLoggedIn(body) {
			return nil, fmt.Errorf("登录后仍无法获取课表数据（响应前 %d 字节: %s）",
				minInt(200, len(body)), strings.TrimSpace(string(body[:minInt(200, len(body))])))
		}
	}
	return body, nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// looksLoggedIn 用课表接口的返回判断会话是否有效：JSON（code 字段）即有效，
// 若为 HTML/401 说明需要登录。
func looksLoggedIn(body []byte) bool {
	return strings.Contains(string(body), `"code"`)
}

func (c *Client) postData(ctx context.Context, semester string) ([]byte, error) {
	u := c.gsmisBase + DefaultDataPath
	form := url.Values{"XNXQDM": {semester}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Origin", c.gsmisBase)
	req.Header.Set("Referer", c.gsmisBase+DefaultIndexPath)
	req.Header.Set("User-Agent", DefaultUserAgent)
	resp, err := c.httpc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求课表数据失败: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return body, fmt.Errorf("未授权（HTTP 401），需要登录")
	}
	if resp.StatusCode != http.StatusOK {
		return body, fmt.Errorf("请求课表数据返回 HTTP %d", resp.StatusCode)
	}
	return body, nil
}

// doLogin 执行完整的 SSO(CAS) 登录流程：
//
//	① GET gsmis 首页 → 302 到 sso /login?service=<回调>；
//	② GET 登录页，解析 execution 表单令牌（验证码默认隐藏）；
//	③ POST 登录表单（username/password/execution/_eventId），跟随重定向
//	   （sso 登录成功后会带 ticket 跳回 gsmis，GS_SESSIONID 等 Cookie
//	   由 CookieJar 自动保存）。
func (c *Client) doLogin(ctx context.Context) error {
	// ① 获取 gsmis → sso 的 service 地址
	noFollow := *c.httpc
	noFollow.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	index := c.gsmisBase + DefaultIndexPath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, index, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", DefaultUserAgent)
	resp, err := noFollow.Do(req)
	if err != nil {
		return fmt.Errorf("访问 gsmis 首页失败: %w", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	loginURL := ""
	if resp.StatusCode == http.StatusFound || resp.StatusCode == http.StatusMovedPermanently {
		loc := resp.Header.Get("Location")
		if strings.Contains(loc, "/login") {
			loginURL = loc
		}
	}
	if loginURL == "" {
		return fmt.Errorf("访问 %s 未得到 SSO 登录跳转（HTTP %d，Location=%q）",
			index, resp.StatusCode, resp.Header.Get("Location"))
	}

	// ② 拉登录页并解析 execution 令牌
	loginReq, err := http.NewRequestWithContext(ctx, http.MethodGet, loginURL, nil)
	if err != nil {
		return err
	}
	loginReq.Header.Set("User-Agent", DefaultUserAgent)
	lr, err := c.httpc.Do(loginReq)
	if err != nil {
		return fmt.Errorf("打开 SSO 登录页失败: %w", err)
	}
	page, err := io.ReadAll(lr.Body)
	lr.Body.Close()
	if err != nil {
		return err
	}
	m := reExecution.FindSubmatch(page)
	if m == nil {
		if reCaptcha.Match(page) {
			return fmt.Errorf("登录页需要验证码（或账号状态异常），无法自动登录，请手动登录一次后复用会话")
		}
		return fmt.Errorf("SSO 登录页未找到 execution 令牌")
	}
	execution := string(m[1])

	// ③ 提交登录表单（action 相对当前登录 URL 解析；若页面自带完整 action 则用之）
	postURL := loginURL
	if am := reFormAction.FindSubmatch(page); am != nil {
		if a := string(am[1]); strings.HasPrefix(a, "http") {
			postURL = a
		} else if strings.HasPrefix(a, "/") {
			u, err := url.Parse(loginURL)
			if err == nil {
				rel := &url.URL{Path: a}
				postURL = u.ResolveReference(rel).String()
			}
		}
	}
	form := url.Values{
		"username":  {c.username},
		"password":  {c.password},
		"execution": {execution},
		"_eventId":  {"submit"},
		"type":      {"username_password"},
	}
	fr, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	fr.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	fr.Header.Set("User-Agent", DefaultUserAgent)
	fr.Header.Set("Referer", loginURL)
	fresp, err := c.httpc.Do(fr)
	if err != nil {
		return fmt.Errorf("提交登录失败: %w", err)
	}
	fbody, _ := io.ReadAll(fresp.Body)
	fresp.Body.Close()

	// 提交失败时服务器会返回登录页并给出错误提示
	if !strings.Contains(fresp.Header.Get("Content-Type"), "text/html") || strings.Contains(string(fbody), "登录失败") || reCaptcha.Match(fbody) {
		hint := extractErrorHint(fbody)
		return fmt.Errorf("SSO 登录未成功（HTTP %d）%s", fresp.StatusCode, hint)
	}
	// 成功：重定向链已把会话 Cookie 存入 Jar；由 FetchJSON 再探测确认
	return nil
}

// extractErrorHint 从登录失败返回的页面里摘取常见提示（用户名密码错误/验证码等）。
func extractErrorHint(page []byte) string {
	for _, kw := range []string{"用户名或密码错误", "密码错误", "用户名不存在", "验证码", "账号已锁定", "locked"} {
		if strings.Contains(string(page), kw) {
			return "：" + kw
		}
	}
	return ""
}
