package schedule

import (
	"strings"
	"testing"
	"time"
)

// TestWeekMaskBits 验证"第 w 周 = 第 w-1 位"的掩码语义，
// 以及 1-3 周有课、第 4 周节假日停课、第 5 周恢复的场景。
func TestWeekMaskBits(t *testing.T) {
	c := Course{WeekMask: NewWeekMask(1, 2, 3, 5)}
	for w := 1; w <= 5; w++ {
		want := w != 4 // 第 4 周停课
		if got := c.HasWeek(w); got != want {
			t.Errorf("HasWeek(%d) = %v, want %v", w, got, want)
		}
	}
	if got := NewWeekMask(1, 2, 3, 5); got != 0b10111 {
		t.Errorf("NewWeekMask(1,2,3,5) = %b, want 10111", got)
	}
}

// TestWeekStartDates 验证每周一日期由第 1 周周一起始、每 7 天推进。
func TestWeekStartDates(t *testing.T) {
	s := NewSampleSchedule()
	first := time.Date(2026, time.September, 7, 0, 0, 0, 0, time.Local)
	for w := 1; w <= s.NumWeeks; w++ {
		want := first.AddDate(0, 0, 7*(w-1))
		if got := s.WeekStart(w); !got.Equal(want) {
			t.Errorf("WeekStart(%d) = %v, want %v", w, got, want)
		}
		if got := s.WeekStart(w).Weekday(); got != time.Monday {
			t.Errorf("WeekStart(%d) 不是周一: %v", w, got)
		}
	}
	// 第 1 周 9/7、第 2 周 9/14、第 13 周 11/30
	if got := s.WeekStart(13).Format("1月2日"); got != "11月30日" {
		t.Errorf("WeekStart(13) = %s, want 11月30日", got)
	}
}

// TestSampleGridCount 验证示例课表的非空格子数 = 121，
// 与《课程表填写示例.xlsx》逐格一致（13 周 × 固定课；详见注释）。
func TestSampleGridCount(t *testing.T) {
	s := NewSampleSchedule()
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	n := 0
	for w := 1; w <= s.NumWeeks; w++ {
		for day := Monday; day <= Sunday; day++ {
			for slot := 1; slot <= SlotsPerDay; slot++ {
				if s.cellText(w, day, slot, RenderOptions{}) != "" {
					n++
				}
			}
		}
	}
	// 26(高数) + 20(线代) + 26(英语) + 2(形势) + 13(体育) + 14(物理) + 20(Python) = 121
	if n != 121 {
		t.Errorf("示例课表非空格子数 = %d, want 121", n)
	}
}

// TestValidateConflict 验证同一格子被两门课占用会被检出。
func TestValidateConflict(t *testing.T) {
	s := &Schedule{
		FirstMonday: time.Date(2026, 9, 7, 0, 0, 0, 0, time.Local),
		NumWeeks:    13,
		Courses: []Course{
			{Name: "A", Weekday: Monday, StartSlot: 1, EndSlot: 1, WeekMask: 1},
			{Name: "B", Weekday: Monday, StartSlot: 1, EndSlot: 1, WeekMask: 1},
		},
	}
	if err := s.Validate(); err == nil || !strings.Contains(err.Error(), "格子冲突") {
		t.Errorf("期望检出格子冲突, got %v", err)
	}
}

// TestRenderHTML 渲染完整网页并检查关键片段：
// 每周一日期表头、课程格、停课周空格、以及整页自包含。
func TestRenderHTML(t *testing.T) {
	s := NewSampleSchedule()
	var b strings.Builder
	if err := s.RenderHTML(&b, RenderOptions{ShowLocation: true}); err != nil {
		t.Fatal(err)
	}
	html := b.String()
	for _, want := range []string{
		"<!DOCTYPE html>", "2026-2027学年第一学期课表（示例）",
		"9月7日", "9月14日", "11月30日", // 周一日期逐列推进
		"高等数学A(上)", "大学物理(上)", "Python程序设计(选修)",
		"rowspan=\"14\"", // 星期列视觉合并
	} {
		if !strings.Contains(html, want) {
			t.Errorf("渲染结果缺少 %q", want)
		}
	}
	// 停课周探测：形势与政策仅第 7 周，则第 6 周周三 11 节应为空
	if got := s.cellText(6, Wednesday, 11, RenderOptions{}); got != "" {
		t.Errorf("第 6 周周三 11 节应无课, got %q", got)
	}
	if got := s.cellText(7, Wednesday, 11, RenderOptions{}); got != "形势与政策" {
		t.Errorf("第 7 周周三 11 节应为形势与政策, got %q", got)
	}
}
