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
	"encoding/base64"
	"encoding/hex"
	"errors"
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
	"github.com/lyy1119/BuaaScheduleRender/login"
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
	// AutoClient 可选：自动登录模式预验证并注入的抓取客户端
	//（由启动方完成登录验证后传入，避免 Web 内重复登录）。
	AutoClient *fetch.Client
	// Version 构建期注入的版本号（出现在 /help 中）。
	Version string
	// Logger 输出日志；nil 时静默（Debug=true 时若未提供将使用 os.Stdout）。
	Logger *log.Logger
}

const sessionCookie = "xskb_sid"

// fetchCacheTTL 会话内课表缓存的过期时间：同一用户同一学期在此时间内直接复用，
// 超过则重新向教务抓取；另可用 ?refresh=1 强制刷新（见 handleSchedule*）。
const fetchCacheTTL = 3 * time.Minute

// Server 是课表 Web 服务。
type Server struct {
	cfg      Config
	clients  *clientTable
	pendings *pendingTable
	mux      *http.ServeMux
	logf     func(string, ...any)
	debugf   func(string, ...any)
}

// pendingLoginTTL 是"等待输入验证码"的登录会话有效期。
const pendingLoginTTL = 5 * time.Minute

// pendingLogin 是一次等待验证码的登录：保存抓取客户端（含 Cookie 会话）、
// 登录尝试（execution + 验证码）、账号密码，供用户填写验证码后继续。
// 仅存于内存，最多保留 pendingLoginTTL，成功/失败后立即删除。
type pendingLogin struct {
	cli     *fetch.Client
	attempt *login.LoginAttempt
	user    string
	pass    string
	expires time.Time
}

type pendingTable struct {
	mu sync.Mutex
	m  map[string]*pendingLogin
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

	// cacheSem/cacheJSON/cacheAt：最近一次成功获取的课表 JSON 缓存
	//（避免登录验证后课表页重复请求同一学期）。缓存在内存中，
	// 有 fetchCacheTTL 有效期，会话被空闲回收即失效。
	cacheSem  string
	cacheJSON []byte
	cacheAt   time.Time
}

func newClientTable(idle time.Duration) *clientTable {
	return &clientTable{m: map[string]*userClient{}, idle: idle}
}

// New 创建服务。
func New(cfg Config) *Server {
	if cfg.SessionIdle <= 0 {
		cfg.SessionIdle = 30 * time.Minute
	}
	if cfg.Version == "" {
		cfg.Version = "dev"
	}
	if cfg.Logger == nil && cfg.Debug {
		cfg.Logger = log.New(log.Writer(), "[web] ", log.LstdFlags)
	}
	s := &Server{
		cfg: cfg, clients: newClientTable(cfg.SessionIdle), pendings: &pendingTable{m: map[string]*pendingLogin{}},
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
	// 机读 API 与帮助文档；方法不匹配时由下面的兜底模式返回 405
	mux.HandleFunc("GET /help", s.handleHelp)
	mux.HandleFunc("/help", s.methodNotAllowed("GET"))
	mux.HandleFunc("GET /api/v1/courses", s.handleAPICourses)
	mux.HandleFunc("/api/v1/courses", s.methodNotAllowed("GET"))
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

// autoClient 返回自动登录模式的全局客户端（懒创建；优先复用注入的 AutoClient）。
func (s *Server) autoClient() *userClient {
	s.clients.mu.Lock()
	defer s.clients.mu.Unlock()
	c, ok := s.clients.m["__auto__"]
	if !ok {
		cli := s.cfg.AutoClient
		if cli == nil {
			cli = fetch.NewClient(s.cfg.Username, s.cfg.Password)
		}
		c = &userClient{cli: cli, lastSeen: time.Now()}
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

func (s *Server) addPending(token string, p *pendingLogin) {
	s.pendings.mu.Lock()
	defer s.pendings.mu.Unlock()
	s.pendings.m[token] = p
}

func (s *Server) getPending(token string) (*pendingLogin, bool) {
	s.pendings.mu.Lock()
	defer s.pendings.mu.Unlock()
	p, ok := s.pendings.m[token]
	if !ok {
		return nil, false
	}
	if time.Now().After(p.expires) {
		delete(s.pendings.m, token)
		return nil, false
	}
	return p, true
}

func (s *Server) dropPending(token string) {
	s.pendings.mu.Lock()
	defer s.pendings.mu.Unlock()
	delete(s.pendings.m, token)
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
		// 换一张验证码：?retry=<token>
		if token := r.URL.Query().Get("retry"); token != "" {
			if p, ok := s.getPending(token); ok {
				ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
				if err := p.attempt.Refresh(ctx); err == nil {
					s.renderCaptchaPage(w, r, p, token, "已更换验证码")
					cancel()
					return
				}
				cancel()
				s.dropPending(token)
				writeLoginPage(w, "验证码已失效，请重新登录")
				return
			}
			writeLoginPage(w, "验证码已过期，请重新登录")
			return
		}
		writeLoginPage(w, "")

	case http.MethodPost:
		if err := r.ParseForm(); err != nil {
			writeLoginPage(w, "表单解析失败")
			return
		}

		// 第二轮：带着验证码继续登录
		if token := strings.TrimSpace(r.FormValue("token")); token != "" {
			p, ok := s.getPending(token)
			if !ok {
				writeLoginPage(w, "验证码已过期，请重新登录")
				return
			}
			captcha := strings.TrimSpace(r.FormValue("captcha"))
			if captcha == "" {
				s.renderCaptchaPage(w, r, p, token, "请输入验证码")
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
			err := p.cli.FinishLogin(ctx, p.attempt, captcha)
			cancel()
			if err != nil {
				if errors.Is(err, login.ErrCaptchaRequired) {
					// 验证码错误或过期：attempt 已刷新出新验证码，继续展示
					s.logf("验证码不正确：账号 %s（%v）", p.user, err)
					s.renderCaptchaPage(w, r, p, token, "验证码不正确或已过期，请重试："+err.Error())
					return
				}
				s.logf("登录失败：账号 %s（%v）", p.user, err)
				s.dropPending(token)
				writeLoginPage(w, "登录失败："+html.EscapeString(err.Error()))
				return
			}
			s.finishLogin(w, r, p.cli, p.user)
			s.dropPending(token)
			return
		}

		// 第一轮：账号密码
		user := strings.TrimSpace(r.FormValue("username"))
		pass := r.FormValue("password")
		if user == "" || pass == "" {
			writeLoginPage(w, "请输入账号与密码")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
		defer cancel()
		cli := fetch.NewClient(user, pass)
		attempt, err := cli.BeginLogin(ctx)
		if err != nil {
			s.logf("登录失败：账号 %s（%v）", user, err)
			writeLoginPage(w, "登录失败："+html.EscapeString(err.Error()))
			return
		}
		// 登录页已要求验证码：保存登录过程并展示验证码
		if attempt.CaptchaRequired() {
			token := s.savePending(cli, attempt, user, pass)
			s.logf("登录需要验证码：账号 %s", user)
			s.renderCaptchaPage(w, r, &pendingLogin{cli: cli, attempt: attempt, user: user}, token,
				"该账号或当前网络触发了统一身份认证验证码，请输入图中字符")
			return
		}
		if err := cli.FinishLogin(ctx, attempt, ""); err != nil {
			if errors.Is(err, login.ErrCaptchaRequired) {
				token := s.savePending(cli, attempt, user, pass)
				s.logf("登录需要验证码：账号 %s", user)
				s.renderCaptchaPage(w, r, &pendingLogin{cli: cli, attempt: attempt, user: user}, token,
					"统一身份认证要求验证码，请输入图中字符")
				return
			}
			s.logf("登录失败：账号 %s（%v）", user, err)
			writeLoginPage(w, "登录失败："+html.EscapeString(err.Error()))
			return
		}
		s.finishLogin(w, r, cli, user)

	default:
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// savePending 保存一次等待验证码的登录，返回其 token。
func (s *Server) savePending(cli *fetch.Client, attempt *login.LoginAttempt, user, pass string) string {
	token := newSID()
	s.addPending(token, &pendingLogin{
		cli: cli, attempt: attempt, user: user, pass: pass,
		expires: time.Now().Add(pendingLoginTTL),
	})
	return token
}

// renderCaptchaPage 取验证码图片并渲染"输入验证码"页面。
func (s *Server) renderCaptchaPage(w http.ResponseWriter, r *http.Request, p *pendingLogin, token, msg string) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	img, mime, err := p.attempt.CaptchaImage(ctx)
	if err != nil {
		s.logf("获取验证码失败：%v", err)
		writeLoginPage(w, "获取验证码失败："+html.EscapeString(err.Error()))
		return
	}
	writeCaptchaLoginPage(w, token, p.user, img, mime, msg)
}

// finishLogin 登录成功后：抓取默认学期课表（同时验证凭据）、建立会话并跳转课表页。
func (s *Server) finishLogin(w http.ResponseWriter, r *http.Request, cli *fetch.Client, user string) {
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	sem := fetch.AutoSemester(time.Now())
	data, err := cli.FetchJSON(ctx, sem)
	if err != nil {
		s.logf("登录失败：账号 %s（%v）", user, err)
		writeLoginPage(w, "登录失败："+html.EscapeString(err.Error()))
		return
	}
	id := newSID()
	uc := &userClient{cli: cli, lastSeen: time.Now(), cacheSem: sem, cacheJSON: data, cacheAt: time.Now()}
	s.addClient(id, uc)
	s.logf("登录成功：账号 %s（session %s…）", user, id[:8])
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: id, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, "/schedule?sem="+sem, http.StatusFound)
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
	refresh := r.URL.Query().Get("refresh") == "1"
	manual := s.cfg.Username == "" // 手动登录模式才显示"退出登录"
	writeSchedulePage(w, sem, manual, refresh)
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
	force := r.URL.Query().Get("refresh") == "1" // 强制绕过缓存重新抓取

	ctx, cancel := context.WithTimeout(r.Context(), 150*time.Second)
	defer cancel()

	data, err := s.scheduleJSON(ctx, uc, sem, force)
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

// methodNotAllowed 返回 405（JSON 错误结构），用于 API/帮助路径的方法兜底。
func (s *Server) methodNotAllowed(allow string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", allow)
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed",
			"该接口只接受 "+allow+" 方法（收到 "+r.Method+"）")
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
  .account { font-size:13px; color:#333; margin:0 0 10px; text-align:center; }
  img.captcha { display:block; margin:8px auto 4px; border:1px solid #c9d2dc; border-radius:4px;
                cursor:pointer; max-width:100%; }
  .captcha-tip { text-align:center; font-size:12px; color:#888; margin:0 0 6px; }
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

// writeCaptchaLoginPage 渲染"输入验证码"页面：显示验证码图片（data URI，无需额外请求）
// 与验证码输入框；hidden token 关联服务端保存的登录会话，支持"换一张"。
func writeCaptchaLoginPage(w http.ResponseWriter, token, user string, img []byte, mime, errMsg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	b64 := base64.StdEncoding.EncodeToString(img)
	escToken := html.EscapeString(token)
	escUser := html.EscapeString(user)
	fmt.Fprint(w, pageBase("登录 - 验证码"))
	fmt.Fprint(w, `<div class="card"><h1>北航课表 · 登录验证码</h1>
  <p class="account">账号：<b>`+escUser+`</b></p>
  <form method="post" action="/login">
    <input type="hidden" name="token" value="`+escToken+`">
    <label>验证码</label>
    <input type="text" name="captcha" autocomplete="off" autofocus required>
    <img class="captcha" src="data:`+html.EscapeString(mime)+`;base64,`+b64+`" alt="验证码"
         onclick="location.href='/login?retry=`+escToken+`'">
    <p class="captcha-tip">看不清？点击图片或 <a href="/login?retry=`+escToken+`">换一张</a></p>
    <button type="submit">继续登录</button>
  </form>`)
	if errMsg != "" {
		fmt.Fprintf(w, `<div class="err">%s</div>`, html.EscapeString(errMsg))
	}
	fmt.Fprint(w, `</div></body></html>`)
}

// writeSchedulePage 输出带工具栏的课表页：左侧学期输入+查询按钮，
// 右侧打印按钮（跳转到纯课表页），下方 iframe 内嵌纯课表。
func writeSchedulePage(w http.ResponseWriter, sem string, manual, refresh bool) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	escSem := html.EscapeString(sem)
	printURL := "/schedule/print?sem=" + escSem
	if refresh {
		printURL += "&refresh=1"
	}
	logout := ""
	if manual {
		logout = `  <a class="btn-logout" href="/logout">退出登录</a>`
	}
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
  .btn-logout { text-decoration:none; padding:7px 16px; border:1px solid #d33; border-radius:4px;
                color:#d33; font-size:14px; margin-left:6px; }
  .btn-logout:hover { background:#fdecec; }
  .btn-refresh { text-decoration:none; padding:7px 12px; border:1px solid #888; border-radius:4px;
                 color:#555; font-size:13px; }
  .btn-refresh:hover { background:#f2f2f2; }
  iframe { display:block; width:100%; height:calc(100vh - 54px); border:0; }
</style></head><body>
<div class="toolbar">
  <span class="brand">北航课表</span>
  <form method="get" action="/schedule">
    <label for="sem">学期</label>
    <input type="text" id="sem" name="sem" value="`+escSem+`" maxlength="5" pattern="[0-9]{5}" title="5 位学期号，如 20261">
    <button type="submit">查询</button>
    <a class="btn-refresh" href="/schedule?sem=`+escSem+`&refresh=1" title="绕过缓存，重新向教务抓取">强制刷新</a>
  </form>
  <span class="spacer"></span>
  <a class="btn-print" target="_blank" href="`+printURL+`">打印</a>`+logout+`
</div>
<iframe src="`+printURL+`" title="课表"></iframe>
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
