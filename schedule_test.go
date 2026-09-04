package schedule

import (
	"strings"
	"testing"
	"time"
)

// TestFitName 验证课程名的严格截断方法：
// 不做"另起别名"，仅按字符数直接截断；数据中的完整课名不受影响。
func TestFitName(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"高等数学A(上)", "高等数学"},
		{"线性代数", "线性代数"}, // 短于预算，原样保留
		{"大学英语(二)", "大学英语"},
		{"Python程序设计(选修)", "Pyth"},
	}
	for _, c := range cases {
		if got := FitName(c.name, CellNameBudget); got != c.want {
			t.Errorf("FitName(%q, %d) = %q, want %q", c.name, CellNameBudget, got, c.want)
		}
	}
	if got := FitName("高等数学", 0); got != "" {
		t.Errorf("FitName 预算 0 应返回空串, got %q", got)
	}
}

// TestWeekMaskBits 验证掩码语义与 1-3 周有课、第 4 周停课、第 5 周恢复。
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
// 起始节次格 = "起始节次 FitName(课名)"；连堂后续节次格 = "*"；停课/无课 = ""。
func TestCellTextRules(t *testing.T) {
	s := NewSampleSchedule()
	cases := []struct {
		week, weekday, slot int
		want                string
	}{
		{1, Monday, 1, "1 高等数学"},    // 高等数学A(上) 截断为 4 字
		{1, Monday, 2, "*"},         // 连堂续节
		{1, Wednesday, 3, "3 高等数学"}, // 周三 3-4 节（同一课程另一元素）
		{1, Wednesday, 4, "*"},
		{1, Friday, 3, "3 大学物理"},      // 大学物理(上) 截断
		{2, Friday, 3, ""},            // 双周停课（单周课）
		{4, Friday, 3, ""},            // 双周停课
		{7, Wednesday, 11, "11 形势与政"}, // 仅第 7 周；5 字课名截 4 字
		{6, Wednesday, 11, ""},
		{2, Tuesday, 6, "6 线性代数"}, // 第 2 周才开始；4 字课名刚好放下
		{2, Tuesday, 7, "*"},
		{1, Saturday, 1, ""}, // Python 第 3 周才开始
	}
	for _, c := range cases {
		if got := s.CellText(c.week, c.weekday, c.slot); got != c.want {
			t.Errorf("CellText(%d,%d,%d) = %q, want %q", c.week, c.weekday, c.slot, got, c.want)
		}
	}
}

// TestCourseInfoSortAndDedup 验证课程信息表：
// 按 CourseID 字典序排序（真实 ID 为非连续字符串）、去重（高数/英语仅一条）。
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
	// M-101（高数两个时间元素）只聚合为一条，且字段完整
	var math *CourseInfo
	for i := range infos {
		if infos[i].CourseID == "M-101" {
			math = &infos[i]
		}
	}
	if math == nil || math.Name != "高等数学A(上)" || math.Teacher == "" || math.Location == "" {
		t.Fatalf("M-101 课程信息不完整: %+v", math)
	}
}

// TestValidateConflict 验证格子冲突与同 CourseID 信息不一致会被检出。
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
	s2 := &Schedule{
		FirstMonday: time.Date(2026, 9, 7, 0, 0, 0, 0, time.Local),
		NumWeeks:    13,
		Elements: []CourseElement{
			{CourseID: "a", Name: "A", Teacher: "x", Weekday: Monday, StartSlot: 1, EndSlot: 1, WeekMask: 1},
			{CourseID: "a", Name: "A", Teacher: "y", Weekday: Wednesday, StartSlot: 1, EndSlot: 1, WeekMask: 1},
		},
	}
	if err := s2.Validate(); err == nil || !strings.Contains(err.Error(), "不一致") {
		t.Errorf("期望检出同 CourseID 信息不一致, got %v", err)
	}
}

// TestRenderHTML 渲染并检查：日期锚定列宽、课程格截断、课程信息表
// （无上课时间、无括号简称注释、按 CourseID 排序并带 1..N 展示序号）。
func TestRenderHTML(t *testing.T) {
	s := NewSampleSchedule()
	var b strings.Builder
	if err := s.RenderHTML(&b); err != nil {
		t.Fatal(err)
	}
	html := b.String()
	for _, want := range []string{
		"<!DOCTYPE html>", "2026-2027学年第一学期课表（示例）",
		"9月7日", "9月14日", "11月30日", "节次-时间表", "08:00-08:45",
		"1 高等数学", "*", "3 高等数学", "11 形势与政",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("渲染结果缺少 %q", want)
		}
	}
	// 课程信息表：7 条、按 CourseID 排序、带 1..7 展示序号
	if got := strings.Count(html, "<li>"); got != 7 {
		t.Errorf("课程信息表条目数 = %d, want 7", got)
	}
	for _, want := range []string{
		`<span class="ci-no">1.</span> <span class="ci-name">Python程序设计(选修)</span>`,
		`<span class="ci-no">3.</span> <span class="ci-name">高等数学A(上)</span>`,
		`<span class="ci-no">7.</span> <span class="ci-name">线性代数</span>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("课程信息表缺少排序条目 %q", want)
		}
	}
	// 信息表不渲染上课时间、不渲染"（简称）"括号注释
	for _, forbid := range []string{"ci-times", "ci-note", "（高数A）", "上课时间"} {
		if strings.Contains(html, forbid) {
			t.Errorf("课程信息表不应包含 %q", forbid)
		}
	}
	// 日期锚定：最长日期形态
	if DateMaxText != "12月31日" {
		t.Errorf("DateMaxText = %q, want 12月31日", DateMaxText)
	}
	// 课程格文本统计："1 高等数学" 应为 13（高数周一 1-2 节，每列起始格）
	if got := strings.Count(html, "1 高等数学"); got != 13 {
		t.Errorf(`"1 高等数学" 出现 %d 次, want 13`, got)
	}
	// 列宽常量：13 个周次列等宽且以日期锚为准
	if weekColWidthPx < 55 {
		t.Errorf("weekColWidthPx = %d 偏窄，日期可能折行", weekColWidthPx)
	}
}
