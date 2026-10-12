package schedule

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// CourseOccurrence 是"某一天某一节课有某门课"的查询结果项（按日期查询用）。
type CourseOccurrence struct {
	CourseID  string   `json:"course_id"`
	Name      string   `json:"name"`
	Teachers  []string `json:"teachers"`
	Locations []string `json:"locations"`
	Weekday   int      `json:"weekday"` // 1=周一 … 7=周日
	StartSlot int      `json:"start_slot"`
	EndSlot   int      `json:"end_slot"`
	StartTime string   `json:"start_time"` // 如 "19:00"；时间表缺失时为空串
	EndTime   string   `json:"end_time"`   // 如 "20:35"
}

// CoursesOn 返回给定日期当天的课程列表（按开始节次排序）。
//
// 计算方式：以 FirstMonday 为第 1 周周一推算该日期属于第几周、星期几，
// 再用各课程元素的周掩码判断当天是否有课。返回值：
//
//	week    该日期所在学期周次（1 起；学期之外可能 <=0 或 >NumWeeks）
//	weekday 星期几（1=周一 … 7=周日）
//	inTerm  该日期是否落在学期范围内（1 ≤ week ≤ NumWeeks）
func (s *Schedule) CoursesOn(date time.Time) (week, weekday int, inTerm bool, courses []CourseOccurrence) {
	loc := s.FirstMonday.Location()
	day := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, loc)
	first := time.Date(s.FirstMonday.Year(), s.FirstMonday.Month(), s.FirstMonday.Day(), 0, 0, 0, 0, loc)

	// 天数差（按自然日计算，避免 DST 造成的 23/25 小时误差）；
	// 周次向下取整，学期之前的天数为负 → week <= 0。
	days := int(math.Floor(day.Sub(first).Hours()/24 + 1e-9))
	week = int(math.Floor(float64(days)/7)) + 1
	if wd := int(day.Weekday()); wd == 0 {
		weekday = 7
	} else {
		weekday = wd
	}
	inTerm = week >= 1 && week <= s.NumWeeks
	if !inTerm {
		return week, weekday, false, nil
	}

	infos := map[string]CourseInfo{}
	for _, ci := range s.CourseInfos() {
		infos[ci.CourseID] = ci
	}
	for i := range s.Elements {
		e := &s.Elements[i]
		if e.Weekday != weekday || !e.HasWeek(week) {
			continue
		}
		ci, ok := infos[e.CourseID]
		name, teachers, locations := e.Name, []string{e.Teacher}, []string{e.Location}
		if ok {
			name, teachers, locations = ci.Name, ci.Teachers, ci.Locations
		}
		oc := CourseOccurrence{
			CourseID: e.CourseID, Name: name,
			Teachers: nonEmpty(teachers), Locations: nonEmpty(locations),
			Weekday: weekday, StartSlot: e.StartSlot, EndSlot: e.EndSlot,
		}
		oc.StartTime, oc.EndTime = s.slotTimeRange(e.StartSlot, e.EndSlot)
		courses = append(courses, oc)
	}
	sort.Slice(courses, func(a, b int) bool {
		if courses[a].StartSlot != courses[b].StartSlot {
			return courses[a].StartSlot < courses[b].StartSlot
		}
		return courses[a].CourseID < courses[b].CourseID
	})
	return week, weekday, true, courses
}

// slotTimeRange 返回 [startSlot, endSlot] 的起止时刻（如 "19:00" / "20:35"）。
// 时间表由源数据 jcfaList 覆盖时以其为准；缺失时返回空串。
func (s *Schedule) slotTimeRange(startSlot, endSlot int) (string, string) {
	start, end := "", ""
	if txt := s.SlotTimeText(startSlot); txt != "" {
		if a, _, ok := strings.Cut(txt, "-"); ok {
			start = a
		}
	}
	if txt := s.SlotTimeText(endSlot); txt != "" {
		if _, b, ok := strings.Cut(txt, "-"); ok {
			end = b
		}
	}
	return start, end
}

// WeekdayName 返回星期几的中文简称（1=周一 … 7=周日）。
func WeekdayName(weekday int) string {
	if weekday < 1 || weekday > 7 {
		return fmt.Sprintf("%d", weekday)
	}
	return []string{"", "周一", "周二", "周三", "周四", "周五", "周六", "周日"}[weekday]
}

func nonEmpty(list []string) []string {
	out := make([]string, 0, len(list))
	for _, v := range list {
		if v != "" {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
