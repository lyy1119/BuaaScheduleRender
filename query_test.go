package schedule

import (
	"testing"
	"time"
)

func day(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.ParseInLocation("2006-01-02", s, time.Local)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// TestCoursesOn 验证"按日期查询当天课程"：周次/星期推算、周掩码过滤与排序。
func TestCoursesOn(t *testing.T) {
	s := NewSampleSchedule() // 第 1 周周一 2026-09-07，共 13 周

	// 第 1 周周一：高数 1-2 节
	week, wd, inTerm, list := s.CoursesOn(day(t, "2026-09-07"))
	if week != 1 || wd != Monday || !inTerm {
		t.Fatalf("2026-09-07 => week=%d wd=%d inTerm=%v", week, wd, inTerm)
	}
	if len(list) != 1 || list[0].CourseID != "M-101" || list[0].StartSlot != 1 || list[0].EndSlot != 2 {
		t.Fatalf("2026-09-07 课程 = %+v", list)
	}
	if list[0].StartTime != "08:00" || list[0].EndTime != "09:35" {
		t.Errorf("时间 = %s-%s, want 08:00-09:35", list[0].StartTime, list[0].EndTime)
	}
	if len(list[0].Teachers) == 0 || len(list[0].Locations) == 0 {
		t.Errorf("教师/教室应来自课程信息: %+v", list[0])
	}

	// 第 1 周周三：大学英语 1-2 与高数 3-4（按开始节次排序）
	_, _, _, list = s.CoursesOn(day(t, "2026-09-09"))
	if len(list) != 2 || list[0].StartSlot != 1 || list[1].StartSlot != 3 {
		t.Fatalf("2026-09-09 课程排序 = %+v", list)
	}

	// 单周课：第 1 周周五有大学物理(P-101)，第 2 周周五没有（另有全周的大学英语）
	hasCourse := func(list []CourseOccurrence, id string) bool {
		for _, c := range list {
			if c.CourseID == id {
				return true
			}
		}
		return false
	}
	if _, _, _, l1 := s.CoursesOn(day(t, "2026-09-11")); !hasCourse(l1, "P-101") {
		t.Errorf("第 1 周周五应含大学物理, got %+v", l1)
	}
	if _, _, _, l2 := s.CoursesOn(day(t, "2026-09-18")); hasCourse(l2, "P-101") {
		t.Errorf("第 2 周周五不应有单周课大学物理, got %+v", l2)
	}

	// 第 7 周周三：含仅第 7 周一次的"形势与政策"
	week, _, _, list = s.CoursesOn(day(t, "2026-10-21"))
	if week != 7 {
		t.Fatalf("2026-10-21 => week=%d, want 7", week)
	}
	found := false
	for _, c := range list {
		if c.CourseID == "P-301" {
			found = true
			if c.StartSlot != 11 || c.EndSlot != 12 {
				t.Errorf("形策节次 = %d-%d, want 11-12", c.StartSlot, c.EndSlot)
			}
		}
	}
	if !found {
		t.Errorf("第 7 周周三应含形势与政策: %+v", list)
	}

	// 周末课：第 3 周周六 Python（第 3-12 周）
	if _, _, _, l := s.CoursesOn(day(t, "2026-09-26")); len(l) != 1 || l[0].CourseID != "CS-501" {
		t.Errorf("第 3 周周六 = %+v", l)
	}

	// 学期范围外
	if w, _, in, _ := s.CoursesOn(day(t, "2026-08-31")); in || w != 0 {
		t.Errorf("开学前应 inTerm=false 且 week<=0, got week=%d inTerm=%v", w, in)
	}
	if _, _, in, _ := s.CoursesOn(day(t, "2026-12-14")); in {
		t.Error("第 15 周超出 13 周学期, 应 inTerm=false")
	}
}

// TestWeekdayName 验证星期中文名。
func TestWeekdayName(t *testing.T) {
	if WeekdayName(Monday) != "周一" || WeekdayName(Sunday) != "周日" {
		t.Error("WeekdayName 输出不正确")
	}
}
