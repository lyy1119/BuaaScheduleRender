package web

import (
	"fmt"
	"net/http"
	"strings"
)

// /help 是服务的机读契约：字段与实现一一对应，供自动化客户端（Agent）
// 直接消费；同一份数据也能渲染成 Markdown 供人阅读。
//
//	GET /help                      → JSON（默认）
//	GET /help?format=md            → Markdown
//	GET /help  Accept: text/plain  → Markdown

type helpDoc struct {
	Service        string          `json:"service"`
	Description    string          `json:"description"`
	Version        string          `json:"version"`
	APIVersion     string          `json:"api_version"`
	Authentication helpAuth        `json:"authentication"`
	QuickStart     []string        `json:"quick_start"`
	Conventions    helpConventions `json:"conventions"`
	Pages          []helpPage      `json:"pages"`
	Endpoints      []helpEndpoint  `json:"endpoints"`
	QueryParams    []helpField     `json:"query_parameters"`
	ResponseFields []helpField     `json:"response_fields"`
	CourseFields   []helpField     `json:"course_fields"`
	ErrorCodes     []helpCode      `json:"error_codes"`
	Examples       []helpExample   `json:"examples"`
	Notes          []string        `json:"notes"`
}

type helpAuth struct {
	Required bool     `json:"required"`
	Modes    []string `json:"modes"`
	Note     string   `json:"note"`
}

type helpConventions struct {
	BasePath         string `json:"base_path"`
	ContentType      string `json:"content_type"`
	DateEncoding     string `json:"date_encoding"`
	TimeEncoding     string `json:"time_encoding"`
	WeekdayEncoding  string `json:"weekday_encoding"`
	SemesterEncoding string `json:"semester_encoding"`
	ErrorShape       string `json:"error_shape"`
	Caching          string `json:"caching"`
	UpstreamCache    string `json:"upstream_cache"`
}

type helpPage struct {
	Path     string `json:"path"`
	Method   string `json:"method"`
	Summary  string `json:"summary"`
	Redirect string `json:"redirect,omitempty"`
}

type helpEndpoint struct {
	Method      string   `json:"method"`
	Path        string   `json:"path"`
	Summary     string   `json:"summary"`
	Parameters  []string `json:"parameters,omitempty"`
	Success     string   `json:"success"`
	Errors      []string `json:"errors,omitempty"`
	CurlExample string   `json:"curl_example,omitempty"`
}

type helpField struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Required    string `json:"required,omitempty"`
	Default     string `json:"default,omitempty"`
	Description string `json:"description"`
}

type helpCode struct {
	Code       string `json:"code"`
	HTTPStatus int    `json:"http_status"`
	Meaning    string `json:"meaning"`
}

type helpExample struct {
	Title    string `json:"title"`
	Request  string `json:"request"`
	Response string `json:"response"`
}

// buildHelp 生成帮助文档（version 为构建期注入的版本号）。
func (s *Server) buildHelp() helpDoc {
	authMode := "session_cookie"
	authNote := "会话 Cookie（登录后由 /login 下发）；未登录访问 API 返回 401。"
	if s.cfg.Username != "" {
		authMode = "server_side_auto_login"
		authNote = "服务以自动登录模式运行（启动参数/环境变量提供账号密码），所有 API 请求无需 Cookie 即可访问。"
	}
	return helpDoc{
		Service:     ServiceName,
		Description: "北航研究生课表服务：自动登录教务系统、抓取并解析课表，提供课表页面、A4 打印页与按日期查询课程的 HTTP API。",
		Version:     s.cfg.Version,
		APIVersion:  APIVersion,
		Authentication: helpAuth{
			Required: true,
			Modes:    []string{authMode},
			Note:     authNote,
		},
		QuickStart: []string{
			"GET /help 获取本契约（JSON；加 ?format=md 或 Accept: text/markdown 得到 Markdown）。",
			"GET /api/v1/courses?date=2026-09-07 查询该日期的课程列表（sem 省略时按日期推断学期）。",
			"浏览器访问 / 进入登录或课表页；/schedule 为课表页，/schedule/print 为纯课表打印页。",
		},
		Conventions: helpConventions{
			BasePath:         "/",
			ContentType:      "application/json; charset=utf-8（API）；text/html（页面）",
			DateEncoding:     "YYYY-MM-DD（本地时区，Asia/Shanghai）",
			TimeEncoding:     "HH:MM（24 小时制，来自课表数据中的节次时间表）",
			WeekdayEncoding:  "1=周一 … 7=周日",
			SemesterEncoding: "5 位学期号，如 20261 = 2026-2027 学年第一学期；第二学期为 xxxx2",
			ErrorShape:       `{"error":{"code":"...","message":"..."}}`,
			Caching:          "所有 API 与页面响应均带防缓存头（Cache-Control: no-store 等）",
			UpstreamCache:    "同一会话内课表数据缓存 3 分钟后自动重新抓取；?refresh=1 可强制绕过缓存",
		},
		Pages: []helpPage{
			{Path: "/", Method: "GET", Summary: "入口：未登录跳转 /login，已登录跳转 /schedule", Redirect: "/login 或 /schedule"},
			{Path: "/login", Method: "GET, POST", Summary: "登录页；POST 账号密码并真实验证，失败停留本页并提示，成功跳转 /schedule", Redirect: "/schedule"},
			{Path: "/schedule", Method: "GET", Summary: "课表页：工具栏（学期查询/强制刷新/打印/退出登录）+ 内嵌课表"},
			{Path: "/schedule/print", Method: "GET", Summary: "纯课表页（A4 HTML，可直接打印）"},
			{Path: "/logout", Method: "GET", Summary: "退出登录并清除会话", Redirect: "/login"},
		},
		Endpoints: []helpEndpoint{
			{
				Method: "GET", Path: "/help",
				Summary:    "本服务的 API 契约（JSON 或 Markdown）",
				Parameters: []string{"format=json|md（可选；md/text 返回 Markdown）", "Accept: text/markdown 或 text/plain（可选，等价于 format=md）"},
				Success:    "200，JSON 文档（默认）或 text/markdown",
				CurlExample: "curl -s http://127.0.0.1:8080/help | jq .\n" +
					"curl -s 'http://127.0.0.1:8080/help?format=md'",
			},
			{
				Method: "GET", Path: "/api/v1/courses",
				Summary: "查询指定日期当天的课程列表（周次、星期、课程名、教师、教室、节次与起止时刻）",
				Parameters: []string{
					"date=YYYY-MM-DD（可选；默认今天）",
					"sem=5 位学期号（可选；省略时按 date 所在学期推断）",
					"refresh=1（可选；强制绕过会话缓存重新抓取）",
				},
				Success: "200，见 response_fields / course_fields",
				Errors: []string{
					"401 unauthorized：未登录（手动登录模式下需先 /login 建立会话）",
					"400 invalid_date：date 不是 YYYY-MM-DD",
					"502 upstream_error：教务数据获取失败（网络、登录失败或风控要求验证码）",
					"502 parse_error：教务返回的数据无法解析",
					"405 method_not_allowed：方法不是 GET",
				},
				CurlExample: "curl -s 'http://127.0.0.1:8080/api/v1/courses?date=2026-09-07' | jq .\n" +
					"curl -s 'http://127.0.0.1:8080/api/v1/courses?date=2026-10-21&sem=20261&refresh=1'",
			},
		},
		QueryParams: []helpField{
			{Name: "date", Type: "string(YYYY-MM-DD)", Required: "no", Default: "今天", Description: "要查询的日期（本地时区）"},
			{Name: "sem", Type: "string(5 位数字)", Required: "no", Default: "按 date 推断", Description: "学期号，如 20261；用于指定从哪个学期取课表"},
			{Name: "refresh", Type: "0|1", Required: "no", Default: "0", Description: "为 1 时绕过会话缓存，强制重新向教务抓取"},
		},
		ResponseFields: []helpField{
			{Name: "service", Type: "string", Description: "服务名，固定 " + ServiceName},
			{Name: "api_version", Type: "string", Description: "API 版本，当前 " + APIVersion},
			{Name: "semester", Type: "string", Description: "本次查询实际使用的学期号"},
			{Name: "date", Type: "string", Description: "查询日期 YYYY-MM-DD"},
			{Name: "week", Type: "integer", Description: "该日期所在学期周次（1 起；学期之外可能 <= 0 或 > num_weeks）"},
			{Name: "in_term", Type: "boolean", Description: "该日期是否落在学期范围内（1 ≤ week ≤ num_weeks）"},
			{Name: "weekday", Type: "integer", Description: "星期几：1=周一 … 7=周日"},
			{Name: "weekday_name", Type: "string", Description: "星期的中文名，如 周一"},
			{Name: "term_start", Type: "string", Description: "学期第 1 周周一日期（由课表数据推算或由服务配置指定）"},
			{Name: "num_weeks", Type: "integer", Description: "学期总周数"},
			{Name: "course_count", Type: "integer", Description: "courses 数组长度"},
			{Name: "courses", Type: "array<object>", Description: "当天课程列表，按开始节次升序；in_term=false 时为空数组"},
		},
		CourseFields: []helpField{
			{Name: "course_id", Type: "string", Description: "课程代码（同一门课的多段安排共享同一代码）"},
			{Name: "name", Type: "string", Description: "课程名（完整名称）"},
			{Name: "teachers", Type: "array<string>", Description: "任课教师（同一课程可有多位）"},
			{Name: "locations", Type: "array<string>", Description: "上课地点（教室）"},
			{Name: "weekday", Type: "integer", Description: "星期几：1=周一 … 7=周日"},
			{Name: "start_slot", Type: "integer", Description: "开始节次（1..14）"},
			{Name: "end_slot", Type: "integer", Description: "结束节次（连堂课程 > start_slot）"},
			{Name: "start_time", Type: "string", Description: "开始时刻 HH:MM（如 19:00）"},
			{Name: "end_time", Type: "string", Description: "结束时刻 HH:MM（如 20:35）"},
		},
		ErrorCodes: []helpCode{
			{Code: "unauthorized", HTTPStatus: 401, Meaning: "未登录或会话过期（手动登录模式）"},
			{Code: "invalid_date", HTTPStatus: 400, Meaning: "date 参数格式错误，应为 YYYY-MM-DD"},
			{Code: "upstream_error", HTTPStatus: 502, Meaning: "获取教务课表数据失败（网络异常、登录失败、风控要求验证码等）"},
			{Code: "parse_error", HTTPStatus: 502, Meaning: "教务返回的数据无法解析为课表"},
			{Code: "method_not_allowed", HTTPStatus: 405, Meaning: "HTTP 方法不受支持（API 只接受 GET）"},
		},
		Examples: []helpExample{
			{
				Title:   "查询某天课程（周五，含单周课）",
				Request: "GET /api/v1/courses?date=2026-09-11",
				Response: `{
  "service": "buaa-schedule",
  "api_version": "1",
  "semester": "20261",
  "date": "2026-09-11",
  "week": 1,
  "in_term": true,
  "weekday": 5,
  "weekday_name": "周五",
  "term_start": "2026-09-07",
  "num_weeks": 19,
  "course_count": 2,
  "courses": [
    {
      "course_id": "P-101",
      "name": "大学物理(上)",
      "teachers": ["陈平"],
      "locations": ["教5-101"],
      "weekday": 5,
      "start_slot": 3,
      "end_slot": 4,
      "start_time": "09:50",
      "end_time": "11:25"
    }
  ]
}`,
			},
			{
				Title:   "学期范围外的日期（in_term=false，courses 为空）",
				Request: "GET /api/v1/courses?date=2026-08-31",
				Response: `{
  "date": "2026-08-31",
  "week": 0,
  "in_term": false,
  "weekday": 1,
  "weekday_name": "周一",
  "course_count": 0,
  "courses": []
}`,
			},
			{
				Title:   "未登录访问（手动登录模式）",
				Request: "GET /api/v1/courses?date=2026-09-07",
				Response: `HTTP/1.1 401 Unauthorized
{"error":{"code":"unauthorized","message":"需要登录：请先访问 /login 建立会话，或让服务以自动登录模式运行"}}`,
			},
		},
		Notes: []string{
			"学期号规则：9 月至次年 1 月为 xxxx1（如 20261），2 月至 8 月为 xxxx2（如 20262）；服务在 sem 省略时按查询日期推断。",
			"学期第 1 周周一由课表数据中课程的首次上课日期推算；若数据中没有任何课程，则退化为当年 1 月 1 日。",
			"节次时间来自教务数据的节次时间表；缺失时 start_time/end_time 为空串。",
			"同一日期可能出现多门课程或多段安排（不同教师/教室），courses 中按 start_slot 升序排列。",
			"连堂课程在课表页以 # 标记后续节次；API 中则体现为 end_slot > start_slot。",
			"教务登录可能触发风控（要求验证码），此时 upstream_error 的 message 会说明原因，稍后重试或改用已登录会话。",
			"页面与 API 同端口提供；课表页与纯课表页都禁止浏览器缓存。",
		},
	}
}

// handleHelp 输出帮助文档：默认 JSON，?format=md 或 Accept: text/markdown|text/plain
// 时输出 Markdown。
func (s *Server) handleHelp(w http.ResponseWriter, r *http.Request) {
	doc := s.buildHelp()
	if wantsMarkdown(r) {
		noCache(w)
		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		_, _ = w.Write([]byte(renderHelpMarkdown(doc)))
		return
	}
	writeJSON(w, http.StatusOK, doc)
}

func wantsMarkdown(r *http.Request) bool {
	accept := strings.ToLower(r.Header.Get("Accept"))
	if strings.Contains(accept, "text/markdown") || strings.Contains(accept, "text/plain") {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format"))) {
	case "md", "markdown", "text", "plain":
		return true
	}
	return false
}

// renderHelpMarkdown 把帮助文档渲染成人类可读的 Markdown。
func renderHelpMarkdown(d helpDoc) string {
	var b strings.Builder
	line := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }

	line("# %s", d.Service)
	line("")
	line("%s", d.Description)
	line("")
	line("- 版本：`%s`（API 版本 `%s`）", d.Version, d.APIVersion)
	line("")
	line("## 快速开始")
	line("")
	for _, q := range d.QuickStart {
		line("- %s", q)
	}
	line("")
	line("## 鉴权")
	line("")
	line("需要鉴权：`%v`；方式：%s", d.Authentication.Required, strings.Join(d.Authentication.Modes, ", "))
	line("")
	line("%s", d.Authentication.Note)
	line("")

	line("## 约定")
	line("")
	line("| 项 | 说明 |")
	line("| --- | --- |")
	for _, row := range [][2]string{
		{"路径前缀", d.Conventions.BasePath},
		{"内容类型", d.Conventions.ContentType},
		{"日期", d.Conventions.DateEncoding},
		{"时间", d.Conventions.TimeEncoding},
		{"星期", d.Conventions.WeekdayEncoding},
		{"学期号", d.Conventions.SemesterEncoding},
		{"错误结构", d.Conventions.ErrorShape},
		{"缓存", d.Conventions.Caching},
		{"上游缓存", d.Conventions.UpstreamCache},
	} {
		line("| %s | %s |", row[0], row[1])
	}
	line("")

	line("## 页面")
	line("")
	line("| 路径 | 方法 | 说明 |")
	line("| --- | --- | --- |")
	for _, p := range d.Pages {
		line("| `%s` | %s | %s |", p.Path, p.Method, p.Summary)
	}
	line("")

	line("## 接口")
	line("")
	for _, e := range d.Endpoints {
		line("### `%s %s`", e.Method, e.Path)
		line("")
		line("%s", e.Summary)
		line("")
		if len(e.Parameters) > 0 {
			line("参数：")
			line("")
			for _, p := range e.Parameters {
				line("- %s", p)
			}
			line("")
		}
		line("成功：%s", e.Success)
		line("")
		if len(e.Errors) > 0 {
			line("错误：")
			line("")
			for _, x := range e.Errors {
				line("- %s", x)
			}
			line("")
		}
		if e.CurlExample != "" {
			line("```bash")
			line("%s", e.CurlExample)
			line("```")
			line("")
		}
	}

	section := func(title string, fields []helpField) {
		line("## %s", title)
		line("")
		line("| 名称 | 类型 | 必填 | 默认 | 说明 |")
		line("| --- | --- | --- | --- | --- |")
		for _, f := range fields {
			line("| `%s` | %s | %s | %s | %s |", f.Name, f.Type, orDash(f.Required), orDash(f.Default), f.Description)
		}
		line("")
	}
	section("请求参数", d.QueryParams)
	section("响应字段", d.ResponseFields)
	section("课程条目字段（courses[]）", d.CourseFields)

	line("## 错误码")
	line("")
	line("| code | HTTP | 含义 |")
	line("| --- | --- | --- |")
	for _, c := range d.ErrorCodes {
		line("| `%s` | %d | %s |", c.Code, c.HTTPStatus, c.Meaning)
	}
	line("")

	line("## 示例")
	line("")
	for _, ex := range d.Examples {
		line("### %s", ex.Title)
		line("")
		line("请求：`%s`", ex.Request)
		line("")
		line("```json")
		line("%s", ex.Response)
		line("```")
		line("")
	}

	line("## 说明")
	line("")
	for _, n := range d.Notes {
		line("- %s", n)
	}
	line("")
	return b.String()
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
