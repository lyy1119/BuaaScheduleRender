package schedule

import (
	"os"
	"testing"
	"time"
)

const xskbSamplePath = "testdata/loadXskbData.json"

func loadXskbSample(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(xskbSamplePath)
	if err != nil {
		t.Fatalf("读取样例数据失败: %v", err)
	}
	return data
}

// TestParseXskb 用真实样例验证 教务数据 → Schedule 的转换：
// 记录 → 课程元素（连堂合并）、ZCBH → 周掩码、CourseID 聚合课程信息。
func TestParseXskb(t *testing.T) {
	data := loadXskbSample(t)
	firstMonday := time.Date(2026, time.September, 7, 0, 0, 0, 0, time.Local)
	s, err := ParseXskb(data, ParseOptions{FirstMonday: firstMonday, NumWeeks: 0})
	if err != nil {
		t.Fatalf("ParseXskb 失败: %v", err)
	}
	if err := s.Validate(); err != nil {
		t.Fatalf("解析结果校验失败: %v", err)
	}
	// 自动周数 = 数据中最大开课周（样例含空间等离子体物理第 18-19 周 → 19）
	if s.NumWeeks != 19 {
		t.Errorf("NumWeeks = %d, want 19（自动取最大开课周）", s.NumWeeks)
	}
	// 标题取自 rwList 的学年学期名
	if s.Title == "课表" || s.Title == "" {
		t.Errorf("标题未使用学年学期信息: %q", s.Title)
	}
	// 合并连堂后元素数 = 12（原始 29 条记录中的相邻节次被合并）
	if len(s.Elements) != 12 {
		t.Errorf("合并后元素数 = %d, want 12", len(s.Elements))
	}
	// 去重后的课程数 = 6（不同 KCDM 数）
	if infos := s.CourseInfos(); len(infos) != 6 {
		t.Errorf("去重后课程数 = %d, want 6", len(infos))
	}
	// 群论 T191041002：周一 11-12 与周四 11-12 两段，各合并为连堂元素；
	// 周一第 5 周停课（1-4,6-17 周），周四第 4 周停课（1-3,5-17 周）
	var mon, thu *CourseElement
	for i := range s.Elements {
		if s.Elements[i].CourseID == "T191041002" {
			switch s.Elements[i].Weekday {
			case Monday:
				mon = &s.Elements[i]
			case Thursday:
				thu = &s.Elements[i]
			}
		}
	}
	if mon == nil || thu == nil {
		t.Fatal("群论应解析出周一与周四两个元素")
	}
	for _, e := range []*CourseElement{mon, thu} {
		if e.Name != "群论" || e.Teacher != "王海龙" || e.Location != "J4-304" {
			t.Errorf("群论信息不完整: %+v", e)
		}
		if e.StartSlot != 11 || e.EndSlot != 12 {
			t.Errorf("群论节次 = %d-%d, want 11-12（连堂合并）", e.StartSlot, e.EndSlot)
		}
	}
	// ZCBH → 掩码：周一含 1-4,6-17 周（第 5 周停课）；周四含 1-3,5-17 周（第 4 周停课）
	if !mon.HasWeek(1) || !mon.HasWeek(4) || mon.HasWeek(5) || !mon.HasWeek(6) || !mon.HasWeek(17) {
		t.Errorf("周一 周掩码错误: 应含 1-4,6-17 周且不含第 5 周")
	}
	if !thu.HasWeek(5) || thu.HasWeek(4) || !thu.HasWeek(3) || !thu.HasWeek(17) {
		t.Errorf("周四 周掩码错误: 应含 1-3,5-17 周且不含第 4 周")
	}
}

// TestParseXskbMultiTeacher 验证同一课程由多位老师分段授课时（真实样例：
// 航天信息技术前沿 四位老师各负责不同周次），解析后课程信息合并为一条。
func TestParseXskbMultiTeacher(t *testing.T) {
	data := loadXskbSample(t)
	s, err := ParseXskb(data, ParseOptions{FirstMonday: time.Date(2026, 9, 7, 0, 0, 0, 0, time.Local)})
	if err != nil {
		t.Fatal(err)
	}
	var info *CourseInfo
	for i := range s.CourseInfos() {
		if s.CourseInfos()[i].CourseID == "D151052023" {
			info = &s.CourseInfos()[i]
		}
	}
	if info == nil {
		t.Fatal("缺少 航天信息技术前沿 课程")
	}
	if len(info.Teachers) != 4 {
		t.Errorf("航天课教师数 = %d, want 4（张振华/程林/李家军/张余）", len(info.Teachers))
	}
}

// TestParseXskbNoMerge 验证不合并时按记录 1:1 生成元素。
func TestParseXskbNoMerge(t *testing.T) {
	data := loadXskbSample(t)
	noMerge := false
	s, err := ParseXskb(data, ParseOptions{
		FirstMonday:   time.Date(2026, 9, 7, 0, 0, 0, 0, time.Local),
		MergeAdjacent: &noMerge,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Elements) != 29 {
		t.Errorf("不合并时元素数 = %d, want 29（= 记录数）", len(s.Elements))
	}
}

// TestParseZCBH 验证周次位串解析与停课周。
func TestParseZCBH(t *testing.T) {
	m, err := parseZCBH("111101111000000000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	c := CourseElement{WeekMask: m}
	for _, w := range []int{1, 2, 3, 4, 6, 7, 8, 9} {
		if !c.HasWeek(w) {
			t.Errorf("第 %d 周应有课", w)
		}
	}
	for _, w := range []int{5, 10, 20, 30} {
		if c.HasWeek(w) {
			t.Errorf("第 %d 周应无课", w)
		}
	}
	if _, err := parseZCBH("111201"); err == nil {
		t.Error("非法位串应报错")
	}
	if _, err := parseZCBH("000000000000000000000000000000"); err == nil {
		t.Error("全 0 位串应报错")
	}
}
