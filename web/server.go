// Package web 提供课表 Web 服务：同一端口提供三个页面，通过跳转衔接：
//
//	GET  /login      登录页（提供账号/密码手动登录）
//	GET  /semester   学期选择页
//	GET  /schedule   课表页（直接返回 render 渲染出的完整 HTML）
//
// 流程：/ → 无会话则跳 /login；登录成功跳 /semester；选定学期跳
// /schedule?sem=20261。若配置了全局账号密码（命令行/环境变量），
// 自动登录模式生效：跳过登录页，/ 与 /login 直接跳 /semester。
//
// 特点：
//   - 课表页响应带强防缓存头（Cache-Control: no-store 等）；
//   - 多用户：每用户独立 fetch.Client（独立 CookieJar），内存会话表
//     RWMutex 保护，支持并发；空闲超时后惰性回收；
//   - 页面均为无 JS 静态 HTML，风格从简。
package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"html"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lyy1119/BuaaScheduleRender"
	"github.com/lyy1119/BuaaScheduleRender/fetch"
	"github.com/lyy1119/BuaaScheduleRender/render"
)

// Config 服务配置。
type Config struct {
	// Username/Password 非空时启用自动登录模式（跳过登录页），
	// 所有请求共用该账号；为空则要求每个用户手动登录。
	Username, Password string
	// FirstMonday 学期第 1 周周一的日期（源数据不含该信息）。
	FirstMonday time.Time
	// Portrait 输出方向（默认 A4 竖向）。
	Portrait bool
	// SessionIdle 会话空闲过期时间；<=0 用默认 30 分钟。
	SessionIdle time.Duration
	// Logger 输出日志；nil 时静默。
	Logger *log.Logger
}

const sessionCookie = "xskb_sid"

// Server 是课表 Web 服务。
type Server struct {
	cfg     Config
	clients *clientTable
	mux     *http.ServeMux
	logf    func(format string, args ...any)
}

type clientTable struct {
	mu   sync.RWMutex
	m    map[string]*userClient
	idle time.Duration
}

// userClient 一个登录用户的抓取客户端（含其独立 CookieJar 会话）。
type userClient struct {
	cli      *fetch.Client
	lastSeen time.Time
	mu       sync.Mutex // 串行化该用户的抓取（自动登录/取数竞争安全）
}

func newClientTable(idle time.Duration) *clientTable {
	return &clientTable{m: map[string]*userClient{}, idle: idle}
}

// New 创建服务。
func New(cfg Config) *Server {
	if cfg.SessionIdle <= 0 {
		cfg.SessionIdle = 30 * time.Minute
	}
	s := &Server{
		cfg:     cfg,
		clients: newClientTable(cfg.SessionIdle),
		logf:    func(string, ...any) {},
	}
	if cfg.Logger != nil {
		s.logf = cfg.Logger.Printf
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleRoot)
	mux.HandleFunc("/login", s.handleLogin)
	mux.HandleFunc("/semester", s.handleSemester)
	mux.HandleFunc("/schedule", s.handleSchedule)
	mux.HandleFunc("/logout", s.handleLogout)
	s.mux = mux
	return s
}

// Handler 返回可供 ListenAndServe 使用的处理器。
func (s *Server) Handler() http.Handler { return s.mux }

// ---- 会话存取 ----

func (s *Server) getClient(id string) (*userClient, bool) {
	s.clients.mu.RLock()
	c, ok := s.clients.m[id]
	if ok {
		c.lastSeen = time.Now()
	}
	s.clients.mu.RUnlock()
	return c, ok
}

func (s *Server) addClient(id string, c *userClient) {
	s.clients.mu.Lock()
	defer s.clients.mu.Unlock()
	s.clients.m[id] = c
}

func (s *Server) removeClient(id string) {
	s.clients.mu.Lock()
	defer s.clients.mu.Unlock()
	delete(s.clients.m, id)
}

// cleanupIdle 惰性清理过期会话（由会话读取路径低频触发）。
func (s *Server) cleanupIdle(now time.Time) {
	s.clients.mu.Lock()
	defer s.clients.mu.Unlock()
	for id, c := range s.clients.m {
		if now.Sub(c.lastSeen) > s.clients.idle {
			delete(s.clients.m, id)
		}
	}
}

// requireUser 根据当前请求确定用户抓取客户端：
// 自动登录模式返回全局客户端；否则按 sid Cookie 查会话（不存在则重定向登录）。
func (s *Server) requireUser(w http.ResponseWriter, r *http.Request) (*userClient, bool) {
	if s.cfg.Username != "" {
		c, ok := s.clients.m["__auto__"]
		if !ok {
			c = &userClient{cli: fetch.NewClient(s.cfg.Username, s.cfg.Password), lastSeen: time.Now()}
			s.clients.mu.Lock()
			s.clients.m["__auto__"] = c
			s.clients.mu.Unlock()
		}
		return c, true
	}
	ck, err := r.Cookie(sessionCookie)
	if err != nil || ck.Value == "" {
		http.Redirect(w, r, "/login", http.StatusFound)
		return nil, false
	}
	s.cleanupIdle(time.Now())
	c, ok := s.getClient(ck.Value)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusFound)
		return nil, false
	}
	return c, true
}

// loggedIn 判断当前请求是否有会话（页面跳转判断用）。
func (s *Server) loggedIn(r *http.Request) bool {
	if s.cfg.Username != "" {
		return true
	}
	ck, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	_, ok := s.getClient(ck.Value)
	return ok
}

// ---- 处理器 ----

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	if s.loggedIn(r) {
		http.Redirect(w, r, "/semester", http.StatusFound)
		return
	}
	http.Redirect(w, r, "/login", http.StatusFound)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	// 自动登录模式：跳过登录页
	if s.cfg.Username != "" {
		http.Redirect(w, r, "/semester", http.StatusFound)
		return
	}
	if s.loggedIn(r) {
		http.Redirect(w, r, "/semester", http.StatusFound)
		return
	}
	noCache(w)
	switch r.Method {
	case http.MethodGet:
		writeLoginPage(w, "")
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			writeLoginPage(w, "表单解析失败")
			return
		}
		user := strings.TrimSpace(r.FormValue("username"))
		pass := r.FormValue("password")
		if user == "" || pass == "" {
			writeLoginPage(w, "请输入账号与密码")
			return
		}
		// 预置会话（登录校验延后到首次取课表，保证页面响应快）
		id := newSID()
		c := &userClient{cli: fetch.NewClient(user, pass), lastSeen: time.Now()}
		s.addClient(id, c)
		http.SetCookie(w, &http.Cookie{
			Name: sessionCookie, Value: id, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		})
		http.Redirect(w, r, "/semester", http.StatusFound)
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleSemester(w http.ResponseWriter, r *http.Request) {
	uc, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	_ = uc
	noCache(w)
	preset := fetch.AutoSemester(time.Now())
	if v := r.URL.Query().Get("sem"); validSemester(v) {
		preset = v
	}
	switch r.Method {
	case http.MethodGet:
		writeSemesterPage(w, preset, "")
	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			writeSemesterPage(w, preset, "表单解析失败")
			return
		}
		sem := strings.TrimSpace(r.FormValue("semester"))
		if !validSemester(sem) {
			writeSemesterPage(w, preset, "学期应为 5 位数字，如 20261")
			return
		}
		http.Redirect(w, r, "/schedule?sem="+sem, http.StatusFound)
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleSchedule 课表页：抓取该学期课表 JSON → 解析 → 渲染 HTML，
// 并附加强防缓存响应头。
func (s *Server) handleSchedule(w http.ResponseWriter, r *http.Request) {
	uc, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sem := r.URL.Query().Get("sem")
	if !validSemester(sem) {
		http.Redirect(w, r, "/semester", http.StatusFound)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 150*time.Second)
	defer cancel()

	uc.mu.Lock()
	data, err := uc.cli.FetchJSON(ctx, sem)
	uc.mu.Unlock()
	if err != nil {
		s.logf("schedule fetch error (sem=%s): %v", sem, err)
		writeSemesterPage(w, sem, "获取课表失败："+html.EscapeString(err.Error()))
		return
	}
	sched, err := schedule.ParseXskb(data, schedule.ParseOptions{FirstMonday: s.cfg.FirstMonday})
	if err != nil {
		s.logf("schedule parse error (sem=%s): %v", sem, err)
		writeSemesterPage(w, sem, "课表解析失败："+html.EscapeString(err.Error()))
		return
	}
	opts := render.RenderOptions{Portrait: s.cfg.Portrait}
	noCache(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := render.RenderHTML(w, sched, opts); err != nil {
		s.logf("render error: %v", err)
	}
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if ck, err := r.Cookie(sessionCookie); err == nil {
		s.removeClient(ck.Value)
		http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1})
	}
	http.Redirect(w, r, "/login", http.StatusFound)
}

// ---- 工具 ----

func validSemester(s string) bool {
	if len(s) != 5 {
		return false
	}
	n, err := strconv.Atoi(s)
	return err == nil && n > 0
}

func newSID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// noCache 设置强防缓存响应头。
func noCache(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
	h.Set("Pragma", "no-cache")
	h.Set("Expires", "0")
}

const pageStyle = `<style>
  body { font-family: "Microsoft YaHei", "PingFang SC", Arial, sans-serif; background:#eef1f5;
         display:flex; justify-content:center; align-items:center; min-height:90vh; margin:0; }
  .card { background:#fff; border:1px solid #d0d7de; border-radius:8px; padding:28px 34px;
          width:320px; box-shadow:0 4px 14px rgba(0,0,0,.08); }
  h1 { font-size:18px; text-align:center; margin:0 0 18px; }
  label { display:block; margin:10px 0 4px; font-size:13px; color:#444; }
  input[type=text], input[type=password] { box-sizing:border-box; width:100%; padding:8px;
          border:1px solid #c9d2dc; border-radius:4px; font-size:14px; }
  button { width:100%; margin-top:18px; padding:9px; border:0; border-radius:4px;
           background:#1f6feb; color:#fff; font-size:14px; cursor:pointer; }
  button:hover { background:#1a5ecb; }
  .err { color:#c0392b; font-size:12px; margin-top:10px; }
  .hint { color:#888; font-size:12px; margin-top:12px; text-align:center; }
  a { color:#1f6feb; }
</style>`

func pageBase(title string) string {
	return "<!DOCTYPE html><html lang=\"zh-CN\"><head><meta charset=\"UTF-8\"><title>" +
		html.EscapeString(title) + "</title>" + pageStyle + "</head><body>"
}

func writeLoginPage(w http.ResponseWriter, errMsg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, pageBase("登录 - 课表"))
	fmt.Fprint(w, `<div class="card"><h1>北航课表 · 登录</h1>
  <form method="post" action="/login">
    <label>账号</label><input type="text" name="username" autocomplete="username" required>
    <label>密码</label><input type="password" name="password" autocomplete="current-password" required>
    <button type="submit">登 录</button>
  </form>`)
	if errMsg != "" {
		fmt.Fprintf(w, `<div class="err">%s</div>`, errMsg)
	}
	fmt.Fprint(w, `</div></body></html>`)
}

func writeSemesterPage(w http.ResponseWriter, preset, errMsg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, pageBase("选择学期 - 课表"))
	fmt.Fprint(w, `<div class="card"><h1>选择学期</h1>
  <form method="post" action="/semester">
    <label>学期编号（5 位，如 20261）</label>
    <input type="text" name="semester" value="`+html.EscapeString(preset)+`">
    <button type="submit">查看课表</button>
  </form>`)
	if errMsg != "" {
		fmt.Fprintf(w, `<div class="err">%s</div>`, errMsg)
	}
	fmt.Fprint(w, `<div class="hint">提示：9-1月为 xxxx1，2-8月为 xxxx2（已自动填入默认值）。</div>`)
	fmt.Fprint(w, `</div></body></html>`)
}
