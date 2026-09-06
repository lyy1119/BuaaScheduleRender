package schedule

import (
	"strings"
	"testing"
	"time"
)

func TestWeekMaskBits(t *testing.T) {
	e := CourseElement{WeekMask: NewWeekMask(1, 2, 3, 5)}
	for w := 1; w <= 5; w++ {
		want := w != 4
		if got := e.HasWeek(w); got != want {
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
		if got := s.WeekStart(w); !got.Equal(first.AddDate(0, 0, 7*(w-1))) {
			t.Errorf("WeekStart(%d) = %v, want %v", w, got, first.AddDate(0, 0, 7*(w-1)))
		}
	}
	if got := s.WeekStart(13).Format("1月2日"); got != "11月30日" {
		t.Errorf("WeekStart(13) = %s, want 11月30日", got)
	}
}

// TestCellTextRules 验证课程格文本规则：
// 课程起始格 = "课程展示序号 FitName(课名)"（序号与右侧课程信息表一致）；
// 连堂后续节次格 = "#"；停课/无课 = ""。
// 示例课表按 CourseID 字典序：CS-501(Python)=1 EN-202(大英)=2 M-101(高数)=3
// P-101(大物)=4 P-301(形策)=5 PE-401(体育)=6 lin-alg-02(线代)=7。

func TestCourseInfoSortAndDedup(t *testing.T) {
	s := NewSampleSchedule()
	infos := s.CourseInfos()
	if len(infos) != 7 {
		t.Fatalf("CourseInfos() 条目数 = %d, want 7", len(infos))
	}
	// 字典序（ASCII）：大写前缀在前，小写 lin-alg-02 排最后
	wantIDs := []string{"CS-501", "EN-202", "M-101", "P-101", "P-301", "PE-401", "lin-alg-02"}
	for i, id := range wantIDs {
		if infos[i].CourseID != id {
			t.Fatalf("排序结果第 %d 项 = %q, want %q（全序=%v）", i+1, infos[i].CourseID, id, wantIDs)
		}
	}
	// 数据中的 CourseID 仍是原字符串，未被改动为数字
	if infos[0].CourseID != "CS-501" {
		t.Errorf("CourseID 不应被改写: %q", infos[0].CourseID)
	}
	// M-101（高数两个时间元素）只聚合为一条，教师/教室分别合并去重
	var math *CourseInfo
	for i := range infos {
		if infos[i].CourseID == "M-101" {
			math = &infos[i]
		}
	}
	if math == nil || math.Name != "高等数学A(上)" {
		t.Fatalf("M-101 课程信息不完整: %+v", math)
	}
	wantTeachers := []string{"王建国", "李敏"}
	wantLocs := []string{"教3-105", "主M-201"}
	if strings.Join(math.Teachers, ",") != strings.Join(wantTeachers, ",") {
		t.Errorf("M-101 教师合并 = %v, want %v", math.Teachers, wantTeachers)
	}
	if strings.Join(math.Locations, ",") != strings.Join(wantLocs, ",") {
		t.Errorf("M-101 教室合并 = %v, want %v", math.Locations, wantLocs)
	}
}

// TestValidateConflict 验证不同课程占用同一格子会被检出。

func TestValidateConflict(t *testing.T) {
	s := &Schedule{
		FirstMonday: time.Date(2026, 9, 7, 0, 0, 0, 0, time.Local),
		NumWeeks:    13,
		Elements: []CourseElement{
			{CourseID: "a", Name: "A", Weekday: Monday, StartSlot: 1, EndSlot: 1, WeekMask: 1},
			{CourseID: "b", Name: "B", Weekday: Monday, StartSlot: 1, EndSlot: 1, WeekMask: 1},
		},
	}
	if err := s.Validate(); err == nil || !strings.Contains(err.Error(), "格子冲突") {
		t.Errorf("期望检出格子冲突, got %v", err)
	}
}

// TestSameIDDifferentTeachersAllowed 验证同一 CourseID 的不同元素允许携带不同的
// 教师/教室（真实场景：分段授课、代课或换教室），由 CourseInfos 合并展示。

func TestSameIDDifferentTeachersAllowed(t *testing.T) {
	s := &Schedule{
		FirstMonday: time.Date(2026, 9, 7, 0, 0, 0, 0, time.Local),
		NumWeeks:    13,
		Elements: []CourseElement{
			{CourseID: "a", Name: "A", Teacher: "x", Location: "101",
				Weekday: Monday, StartSlot: 1, EndSlot: 1, WeekMask: 1},
			{CourseID: "a", Name: "A", Teacher: "y", Location: "202",
				Weekday: Wednesday, StartSlot: 1, EndSlot: 1, WeekMask: 1},
		},
	}
	if err := s.Validate(); err != nil {
		t.Errorf("同 CourseID 不同教师/教室不应报错, got %v", err)
	}
	infos := s.CourseInfos()
	if len(infos) != 1 || len(infos[0].Teachers) != 2 || len(infos[0].Locations) != 2 {
		t.Errorf("聚合信息不正确: %+v", infos)
	}
}

// TestRenderHTML 渲染并检查：日期锚定列宽、课程格截断、课程信息表
// （无上课时间、无括号简称注释、按 CourseID 排序并带 1..N 展示序号）。
