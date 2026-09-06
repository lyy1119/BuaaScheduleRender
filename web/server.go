// Package web 提供课表 Web 服务：同一端口、页面间用跳转衔接。
//
//	GET  /login    登录页（提供账号/密码手动登录）
//	GET  /schedule 课表页：上方工具栏（左侧学期输入+查询按钮，右侧打印按钮
//	                跳转到纯课表页），下方以 iframe 内嵌课表
//	GET  /schedule/print 纯课表页（A4 完整 HTML，用于直接打印）
//	GET  /logout   退出
//
// 未提供全局账号密码时，/ 与 /schedule 会跳 /login；提供（命令行/环境变量）
// 时自动登录模式生效，跳过登录页。登录后直接进入 /schedule（学期默认按当前
// 时间推断，工具栏可随时改学期查询）。
//
// 特点：课表页与纯课表页都带强防缓存头（Cache-Control: no-store 等）；
// 多用户并发：每用户独立 fetch.Client（独立 Cookie 会话），会话表 RWMutex
// 保护 + 空闲超时惰性回收；页面无 JS。
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
	// Username/Password 非空时启用自动登录模式（跳过登录页）。
	Username, Password string
	// FirstMonday 学期第 1 周周一的日期；零值由解析时按源数据自动推算。
	FirstMonday time.Time
	// Portrait 输出方向（默认 A4 竖向）。
	Portrait bool
	// SessionIdle 会话空闲过期时间；<=0 用默认 30 分钟。
	SessionIdle time.Duration
	// Debug 开启调试日志：每次 HTTP 请求(访问日志)、fetch 抓取详情等；
	// 登录成功/失败、登出等关键事件在普通模式也会输出（若提供 Logger）。
	Debug bool
	// Logger 输出日志；nil 时静默（Debug=true 时若未提供将使用 os.Stdout）。
	Logger *log.Logger
}

const sessionCookie = "xskb_sid"

// Server 是课表 Web 服务。
type Server struct {
	cfg     Config
	clients *clientTable
	mux     *http.ServeMux
	logf    func(string, ...any)
	debugf  func(string, ...any)
}

type clientTable struct {
	mu   sync.RWMutex
	m    map[string]*userClient
	idle time.Duration
}

// userClient 一个登录用户的抓取客户端（含独立 Cookie 会话）。
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
	if cfg.Logger == nil && cfg.Debug {
		cfg.Logger = log.New(log.Writer(), "[web] ", log.LstdFlags)
	}
	s := &Server{
		cfg: cfg, clients: newClientTable(cfg.SessionIdle),
		logf:   func(string, ...any) {},
		debugf: func(string, ...any) {},
	}
	if cfg.Logger != nil {
		s.logf = cfg.Logger.Printf
		if cfg.Debug {
			s.debugf = cfg.Logger.Printf
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleRoot)
	mux.HandleFunc("/login", s.handleLogin)
	mux.HandleFunc("/logout", s.handleLogout)
	mux.HandleFunc("/schedule", s.handleSchedule)
	mux.HandleFunc("/schedule/print", s.handleSchedulePrint)
	s.mux = mux
	return s
}

// Handler 返回可供 ListenAndServe 使用的处理器。
// Debug 模式会包一层访问日志（方法/路径/状态/耗时/来源）。
func (s *Server) Handler() http.Handler {
	if !s.cfg.Debug {
		return s.mux
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		s.mux.ServeHTTP(rec, r)
		s.debugf("%s %s?%s -> %d (%dB, %s) from %s",
			r.Method, r.URL.Path, r.URL.RawQuery, rec.status, rec.bytes, time.Since(start).Round(time.Millisecond), r.RemoteAddr)
	})
}

// statusRecorder 记录响应状态码与字节数，供访问日志使用。
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(p []byte) (int, error) {
	r.bytes += len(p)
	return r.ResponseWriter.Write(p)
}

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

func (s *Server) cleanupIdle(now time.Time) {
	s.clients.mu.Lock()
	defer s.clients.mu.Unlock()
	for id, c := range s.clients.m {
		if now.Sub(c.lastSeen) > s.clients.idle {
			delete(s.clients.m, id)
		}
	}
}

// autoClient 返回自动登录模式的全局客户端（懒创建）。
func (s *Server) autoClient() *userClient {
	s.clients.mu.Lock()
	defer s.clients.mu.Unlock()
	c, ok := s.clients.m["__auto__"]
	if !ok {
		c = &userClient{cli: fetch.NewClient(s.cfg.Username, s.cfg.Password), lastSeen: time.Now()}
		s.clients.m["__auto__"] = c
		s.debugf("自动登录会话创建：账号 %s", s.cfg.Username)
	}
	return c
}

// requireUser 确定当前请求的用户客户端：
// 自动登录模式返回全局客户端；否则按 sid Cookie 查会话（不存在则跳 /login）。
func (s *Server) requireUser(w http.ResponseWriter, r *http.Request) (*userClient, bool) {
	if s.cfg.Username != "" {
		return s.autoClient(), true
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
		http.Redirect(w, r, "/schedule", http.StatusFound)
		return
	}
	http.Redirect(w, r, "/login", http.StatusFound)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Username != "" || s.loggedIn(r) {
		http.Redirect(w, r, "/schedule", http.StatusFound)
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
		id := newSID()
		s.addClient(id, &userClient{cli: fetch.NewClient(user, pass), lastSeen: time.Now()})
		s.logf("登录：账号 %s（session %s…）", user, id[:8])
		http.SetCookie(w, &http.Cookie{
			Name: sessionCookie, Value: id, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		})
		http.Redirect(w, r, "/schedule", http.StatusFound)
	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if ck, err := r.Cookie(sessionCookie); err == nil {
		s.logf("登出：session %s…", ck.Value[:minInt(8, len(ck.Value))])
		s.removeClient(ck.Value)
		http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1})
	}
	http.Redirect(w, r, "/login", http.StatusFound)
}

// handleSchedule 课表页：上方工具栏（学期输入+查询 | 打印按钮），
// 下方 iframe 内嵌纯课表页。学期缺省按当前时间推断。
func (s *Server) handleSchedule(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	noCache(w)
	sem := semesterOrAuto(r.URL.Query().Get("sem"))
	writeSchedulePage(w, sem)
}

// handleSchedulePrint 纯课表页（用于打印；同样防缓存）。
func (s *Server) handleSchedulePrint(w http.ResponseWriter, r *http.Request) {
	uc, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	sem := semesterOrAuto(r.URL.Query().Get("sem"))

	ctx, cancel := context.WithTimeout(r.Context(), 150*time.Second)
	defer cancel()

	s.debugf("fetch 开始：sem=%s", sem)
	fstart := time.Now()
	uc.mu.Lock()
	data, err := uc.cli.FetchJSON(ctx, sem)
	uc.mu.Unlock()
	if err != nil {
		s.debugf("fetch 失败：sem=%s（%s）", sem, err)
	} else {
		s.debugf("fetch 完成：sem=%s，%d 字节，%s", sem, len(data), time.Since(fstart).Round(time.Millisecond))
	}
	if err != nil {
		s.logf("fetch error (sem=%s): %v", sem, err)
		noCache(w)
		writeErrorPage(w, "获取课表失败："+html.EscapeString(err.Error()))
		return
	}
	sched, err := schedule.ParseXskb(data, schedule.ParseOptions{FirstMonday: s.cfg.FirstMonday})
	if err != nil {
		s.logf("parse error (sem=%s): %v", sem, err)
		noCache(w)
		writeErrorPage(w, "课表解析失败："+html.EscapeString(err.Error()))
		return
	}
	noCache(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	s.debugf("渲染课表：sem=%s，%d 周，%d 门课", sem, sched.NumWeeks, len(sched.CourseInfos()))
	opts := render.RenderOptions{Portrait: s.cfg.Portrait}
	if err := render.RenderHTML(w, sched, opts); err != nil {
		s.logf("render error: %v", err)
	}
}

// ---- 工具 ----

func semesterOrAuto(v string) string {
	if validSemester(v) {
		return v
	}
	return fetch.AutoSemester(time.Now())
}

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
          width:340px; box-shadow:0 4px 14px rgba(0,0,0,.08); }
  h1 { font-size:18px; text-align:center; margin:0 0 18px; }
  label { display:block; margin:10px 0 4px; font-size:13px; color:#444; }
  input[type=text], input[type=password] { box-sizing:border-box; width:100%; padding:8px;
          border:1px solid #c9d2dc; border-radius:4px; font-size:14px; }
  button { width:100%; margin-top:18px; padding:9px; border:0; border-radius:4px;
           background:#1f6feb; color:#fff; font-size:14px; cursor:pointer; }
  button:hover { background:#1a5ecb; }
  .err { color:#c0392b; font-size:12px; margin-top:10px; }
  .hint { color:#888; font-size:12px; margin-top:12px; text-align:center; }
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

// writeSchedulePage 输出带工具栏的课表页：左侧学期输入+查询按钮，
// 右侧打印按钮（跳转到纯课表页），下方 iframe 内嵌纯课表。
func writeSchedulePage(w http.ResponseWriter, sem string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	escSem := html.EscapeString(sem)
	fmt.Fprint(w, `<!DOCTYPE html><html lang="zh-CN"><head><meta charset="UTF-8">
<title>课表 `+escSem+`</title>
<style>
  body { font-family: "Microsoft YaHei", "PingFang SC", Arial, sans-serif; margin:0; background:#eef1f5; }
  .toolbar { display:flex; align-items:center; gap:16px; flex-wrap:wrap; background:#fff;
             border-bottom:1px solid #d0d7de; padding:8px 14px; }
  .brand { font-size:16px; font-weight:bold; }
  .toolbar form { display:flex; align-items:center; gap:6px; margin:0; }
  .toolbar input[type=text] { width:86px; padding:6px 8px; border:1px solid #c9d2dc; border-radius:4px; font-size:14px; }
  .toolbar button { padding:7px 16px; border:0; border-radius:4px; background:#1f6feb; color:#fff;
                    font-size:14px; cursor:pointer; }
  .spacer { flex:1; }
  .btn-print { text-decoration:none; padding:7px 16px; border:1px solid #1f6feb; border-radius:4px;
               color:#1f6feb; font-size:14px; }
  .btn-print:hover { background:#eaf2ff; }
  iframe { display:block; width:100%; height:calc(100vh - 54px); border:0; }
</style></head><body>
<div class="toolbar">
  <span class="brand">北航课表</span>
  <form method="get" action="/schedule">
    <label for="sem">学期</label>
    <input type="text" id="sem" name="sem" value="`+escSem+`" maxlength="5" pattern="[0-9]{5}" title="5 位学期号，如 20261">
    <button type="submit">查询</button>
  </form>
  <span class="spacer"></span>
  <a class="btn-print" target="_blank" href="/schedule/print?sem=`+escSem+`">打印</a>
</div>
<iframe src="/schedule/print?sem=`+escSem+`" title="课表"></iframe>
</body></html>`)
}

// writeErrorPage 纯课表页出错时的简单提示页（无 JS，可显示在 iframe 内）。
func writeErrorPage(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, pageBase("错误 - 课表"))
	fmt.Fprint(w, `<div class="card"><h1>课表加载失败</h1><div class="err">`+msg+`</div>
<div class="hint"><a href="/schedule">返回重试</a></div></div></body></html>`)
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
