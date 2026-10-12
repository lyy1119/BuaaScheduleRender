package web

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/lyy1119/BuaaScheduleRender"
	"github.com/lyy1119/BuaaScheduleRender/fetch"
)

// APIVersion 是 HTTP API 的版本（路径中的 /api/v1 与之对应）。
const APIVersion = "1"

// ServiceName 出现在 /help 与 API 响应中。
const ServiceName = "buaa-schedule"

// scheduleJSON 返回课表原始 JSON：命中会话缓存（TTL 内）时直接复用，
// 否则向教务抓取并更新缓存；force=true 强制绕过缓存。
func (s *Server) scheduleJSON(ctx context.Context, uc *userClient, sem string, force bool) ([]byte, error) {
	now := time.Now()
	uc.mu.Lock()
	defer uc.mu.Unlock()
	switch {
	case !force && uc.cacheSem == sem && now.Sub(uc.cacheAt) < fetchCacheTTL:
		s.debugf("fetch 命中缓存：sem=%s（缓存 %s 前）", sem, now.Sub(uc.cacheAt).Round(time.Second))
		return uc.cacheJSON, nil
	case uc.cacheSem == sem && !force:
		s.debugf("fetch 缓存过期：sem=%s（%s 前，将重新抓取）", sem, now.Sub(uc.cacheAt).Round(time.Second))
	default:
		suffix := ""
		if force {
			suffix = "（强制刷新）"
		}
		s.debugf("fetch 开始：sem=%s%s", sem, suffix)
	}
	data, err := uc.cli.FetchJSON(ctx, sem)
	if err != nil {
		s.debugf("fetch 失败：sem=%s（%s）", sem, err)
		return nil, err
	}
	uc.cacheSem, uc.cacheJSON, uc.cacheAt = sem, data, now
	return data, nil
}

// coursesResponse 是 GET /api/v1/courses 的响应体。
type coursesResponse struct {
	Service     string                      `json:"service"`
	APIVersion  string                      `json:"api_version"`
	Semester    string                      `json:"semester"`
	Date        string                      `json:"date"`
	Week        int                         `json:"week"`
	InTerm      bool                        `json:"in_term"`
	Weekday     int                         `json:"weekday"`
	WeekdayName string                      `json:"weekday_name"`
	TermStart   string                      `json:"term_start"`
	NumWeeks    int                         `json:"num_weeks"`
	CourseCount int                         `json:"course_count"`
	Courses     []schedule.CourseOccurrence `json:"courses"`
}

type apiErrorBody struct {
	Error apiError `json:"error"`
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// writeJSON 输出 JSON 响应（带防缓存头）。
func writeJSON(w http.ResponseWriter, status int, v any) {
	noCache(w)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeAPIError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, apiErrorBody{Error: apiError{Code: code, Message: msg}})
}

// requireAPIUser 与 requireUser 类似，但未登录时返回 401 JSON（而不是重定向）。
func (s *Server) requireAPIUser(w http.ResponseWriter, r *http.Request) (*userClient, bool) {
	if s.cfg.Username != "" {
		return s.autoClient(), true
	}
	ck, err := r.Cookie(sessionCookie)
	if err != nil || ck.Value == "" {
		writeAPIError(w, http.StatusUnauthorized, "unauthorized",
			"需要登录：请先访问 /login 建立会话，或让服务以自动登录模式运行")
		return nil, false
	}
	uc, ok := s.getClient(ck.Value)
	if !ok {
		writeAPIError(w, http.StatusUnauthorized, "unauthorized", "会话不存在或已过期，请重新登录")
		return nil, false
	}
	return uc, true
}

// handleAPICourses 实现 GET /api/v1/courses?date=YYYY-MM-DD[&sem=20261][&refresh=1]：
// 返回指定日期当天的课程列表。
func (s *Server) handleAPICourses(w http.ResponseWriter, r *http.Request) {
	uc, ok := s.requireAPIUser(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()

	dateStr := q.Get("date")
	if dateStr == "" {
		dateStr = time.Now().Format("2006-01-02")
	}
	date, err := time.ParseInLocation("2006-01-02", dateStr, time.Local)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_date",
			"date 必须是 YYYY-MM-DD 格式（例如 2026-09-07）")
		return
	}

	sem := q.Get("sem")
	if !validSemester(sem) {
		// 未指定或非法：按查询日期所在学期推断
		sem = fetch.AutoSemester(date)
	}
	force := q.Get("refresh") == "1"

	ctx, cancel := context.WithTimeout(r.Context(), 150*time.Second)
	defer cancel()

	data, err := s.scheduleJSON(ctx, uc, sem, force)
	if err != nil {
		s.logf("api fetch error (sem=%s): %v", sem, err)
		writeAPIError(w, http.StatusBadGateway, "upstream_error",
			"获取课表数据失败："+err.Error())
		return
	}
	sched, err := schedule.ParseXskb(data, schedule.ParseOptions{FirstMonday: s.cfg.FirstMonday})
	if err != nil {
		s.logf("api parse error (sem=%s): %v", sem, err)
		writeAPIError(w, http.StatusBadGateway, "parse_error", "课表数据解析失败："+err.Error())
		return
	}

	week, weekday, inTerm, courses := sched.CoursesOn(date)
	if courses == nil {
		courses = []schedule.CourseOccurrence{}
	}
	resp := coursesResponse{
		Service: ServiceName, APIVersion: APIVersion,
		Semester: sem, Date: date.Format("2006-01-02"),
		Week: week, InTerm: inTerm, Weekday: weekday, WeekdayName: schedule.WeekdayName(weekday),
		TermStart: sched.FirstMonday.Format("2006-01-02"), NumWeeks: sched.NumWeeks,
		CourseCount: len(courses), Courses: courses,
	}
	s.debugf("api courses：date=%s sem=%s week=%d in_term=%v 课程数=%d", resp.Date, sem, week, inTerm, len(courses))
	writeJSON(w, http.StatusOK, resp)
}
