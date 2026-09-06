// Package fetch 负责从北航研究生教务 GSMIS 抓取课表原始 JSON。
//
//	Client 使用 login.Session 复用登录会话；会话失效（HTTP 401）时，
//	自动以账号密码完成 SSO 登录后重试一次。
//	AutoSemester 按当前时间推断学期号（默认两学期）。
//
// 返回的 JSON 结构同 testdata/loadXskbData.json，可交给根包
// schedule.ParseXskb 继续解析为课表。
package fetch

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lyy1119/BuaaScheduleRender/login"
)

// 默认服务地址与接口路径。
const (
	DefaultGsmisBase = "https://gsmis.buaa.edu.cn"
	DefaultIndexPath = "/gsapp/sys/wdkbapp/*default/index.do"
	DefaultDataPath  = "/gsapp/sys/wdkbapp/bykb/loadXskbData.do"
)

// Client 抓取 GSMIS 课表数据的客户端。
type Client struct {
	user, pass string
	session    *login.Session
	gsmisBase  string
}

// NewClient 构造客户端。未登录时首次 FetchJSON 会用账号密码自动登录。
func NewClient(username, password string) *Client {
	return &Client{
		user:      username,
		pass:      password,
		session:   login.NewSession(),
		gsmisBase: DefaultGsmisBase,
	}
}

// Session 返回底层登录会话（可复用 Cookie、注入已有会话等）。
func (c *Client) Session() *login.Session { return c.session }

// AddRawCookieHeader 复用浏览器已登录会话的 Cookie（见 login.Session）。
func (c *Client) AddRawCookieHeader(raw string) error {
	return c.session.AddRawCookieHeader(raw, c.gsmisBase, login.DefaultSSOLogin)
}

// CookieHeader 导出当前会话 Cookie（见 login.Session）。
func (c *Client) CookieHeader() string {
	return c.session.CookieHeader(c.gsmisBase, login.DefaultSSOLogin)
}

// AutoSemester 根据当前时间推断学期号：
//
//	9 月 ~ 12 月：本学年第一学期 → 20261
//	1 月：          上一学年第一学期 → 20251
//	2 月 ~ 8 月：    上一学年第二学期 → 20252
//
// 当前按一年两个学期处理（第三学期可后续扩展）。
func AutoSemester(now time.Time) string {
	switch m := now.Month(); {
	case m >= 9:
		return fmt.Sprintf("%d1", now.Year())
	case m == 1:
		return fmt.Sprintf("%d1", now.Year()-1)
	default:
		return fmt.Sprintf("%d2", now.Year()-1)
	}
}

// FetchJSON 返回指定学期课表 JSON 字节。semester 为 5 位学期号（如 "20261"），
// 传空串时按 AutoSemester(time.Now()) 推断。
func (c *Client) FetchJSON(ctx context.Context, semester string) ([]byte, error) {
	if semester == "" {
		semester = AutoSemester(time.Now())
	}
	body, err := c.postData(ctx, semester)
	if err != nil {
		return nil, err
	}
	if !looksLoggedIn(body) {
		// 会话失效/未登录 → 自动登录后重试一次
		if err := c.ensureLoggedIn(ctx); err != nil {
			return nil, err
		}
		body, err = c.postData(ctx, semester)
		if err != nil {
			return nil, err
		}
		if !looksLoggedIn(body) {
			snip := strings.TrimSpace(string(body))
			if len(snip) > 200 {
				snip = snip[:200]
			}
			return nil, fmt.Errorf("登录后仍无法获取课表数据（响应: %s）", snip)
		}
	}
	return body, nil
}

// ensureLoggedIn 访问 gsmis 触发跳转拿到 SSO service 地址，然后自动登录。
func (c *Client) ensureLoggedIn(ctx context.Context) error {
	index := c.gsmisBase + DefaultIndexPath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, index, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", login.DefaultUserAgent)
	noFollow := *c.session.HTTP
	noFollow.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := noFollow.Do(req)
	if err != nil {
		return fmt.Errorf("访问 %s 失败: %w", index, err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	loginURL := ""
	if (resp.StatusCode == http.StatusFound || resp.StatusCode == http.StatusMovedPermanently) &&
		strings.Contains(resp.Header.Get("Location"), "/login") {
		loginURL = resp.Header.Get("Location")
	}
	if loginURL == "" {
		return fmt.Errorf("访问 %s 未得到 SSO 跳转（HTTP %d）", index, resp.StatusCode)
	}
	return c.session.CASLogin(ctx, loginURL, c.user, c.pass)
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
	req.Header.Set("User-Agent", login.DefaultUserAgent)
	resp, err := c.session.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求课表数据失败: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return body, nil // 会话失效信号，交由 FetchJSON 触发自动登录
	}
	if resp.StatusCode != http.StatusOK {
		return body, fmt.Errorf("请求课表数据返回 HTTP %d", resp.StatusCode)
	}
	return body, nil
}

// looksLoggedIn 以接口是否返回 JSON（含 "code"）判断会话是否有效。
func looksLoggedIn(body []byte) bool {
	return strings.Contains(string(body), `"code"`)
}
