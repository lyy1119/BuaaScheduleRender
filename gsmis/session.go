package gsmis

import (
	"net/http"
	"net/url"
	"strings"
)

// AddRawCookieHeader 把浏览器/抓包得到的 Cookie 请求头字符串
// （形如 "GS_SESSIONID=...; _WEU=...; ..."）注入当前会话的 CookieJar，
// 用于复用已登录的会话（如手动登录一次后自动续用），
// Cookie 会同时关联到 gsmis 与 sso 两个域。
func (c *Client) AddRawCookieHeader(raw string) error {
	cookies := parseRawCookieHeader(raw)
	hosts := []string{c.gsmisBase}
	if u, err := url.Parse(DefaultSSOLogin); err == nil {
		hosts = append(hosts, u.Scheme+"://"+u.Host)
	}
	for _, host := range hosts {
		u, err := url.Parse(host)
		if err != nil {
			return err
		}
		c.httpc.Jar.SetCookies(u, cookies)
	}
	return nil
}

// CookieHeader 导出当前会话的 Cookie 头（用于调试/其它工具复用）。
func (c *Client) CookieHeader() string {
	var parts []string
	seen := map[string]bool{}
	collect := func(u *url.URL) {
		for _, ck := range c.httpc.Jar.Cookies(u) {
			key := ck.Name + "=" + ck.Value
			if !seen[key] {
				seen[key] = true
				parts = append(parts, key)
			}
		}
	}
	if u, err := url.Parse(c.gsmisBase); err == nil {
		collect(u)
	}
	if u, err := url.Parse(DefaultSSOLogin); err == nil {
		collect(u)
	}
	return strings.Join(parts, "; ")
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
		out = append(out, &http.Cookie{
			Name:  strings.TrimSpace(kv[0]),
			Value: strings.TrimSpace(kv[1]),
			Path:  "/",
		})
	}
	return out
}
