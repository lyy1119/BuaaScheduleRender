package schedule

import (
	"encoding/json"
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
	// 课程信息直接来自 rwList 的 RKJS（数据源已聚合），顺序=源数据顺序
	wantTeachers := []string{"张振华", "程林", "李家军", "张余"}
	if len(info.Teachers) != len(wantTeachers) {
		t.Errorf("航天课教师数 = %d, want 4", len(info.Teachers))
	}
	for i, w := range wantTeachers {
		if info.Teachers[i] != w {
			t.Fatalf("航天课教师 = %v, want %v", info.Teachers, wantTeachers)
		}
	}
	// 教室从 rwList.PKSJDD 提取（去重）
	if len(info.Locations) != 1 || info.Locations[0] != "SH2-104" {
		t.Errorf("航天课教室 = %v, want [SH2-104]", info.Locations)
	}
	// Schedule.CourseList 已填充：课程信息表不再依赖元素二次聚合
	if len(s.CourseList) != 6 {
		t.Errorf("Schedule.CourseList 长度 = %d, want 6", len(s.CourseList))
	}
	for i, ci := range s.CourseInfos() {
		if i > 0 && s.CourseInfos()[i-1].CourseID > ci.CourseID {
			t.Error("CourseInfos 应按 CourseID 字典序排列")
		}
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

// TestParseJCFASlotTimes 验证样例数据中的 jcfaList 时间表被解析并覆盖默认时间表。
func TestParseJCFASlotTimes(t *testing.T) {
	data := loadXskbSample(t)
	s, err := ParseXskb(data, ParseOptions{FirstMonday: time.Date(2026, 9, 7, 0, 0, 0, 0, time.Local)})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.SlotTimes) != 14 {
		t.Fatalf("解析出的时间表长度 = %d, want 14", len(s.SlotTimes))
	}
	for slot, want := range map[int]string{
		1:  "08:00-08:45",
		10: "17:30-18:15",
		11: "19:00-19:45",
		14: "21:30-22:15",
	} {
		if got := s.SlotTimeText(slot); got != want {
			t.Errorf("第 %d 节时间 = %q, want %q", slot, got, want)
		}
	}
}

// TestSlotTimeOverride 验证 SlotTimeText 的取值优先级：
// 课表自带时间表覆盖默认；未设置时回退默认。
func TestSlotTimeOverride(t *testing.T) {
	base := &Schedule{FirstMonday: time.Date(2026, 9, 7, 0, 0, 0, 0, time.Local), NumWeeks: 13}
	if got := base.SlotTimeText(1); got != "08:00-08:45" {
		t.Errorf("未覆盖时应使用默认第 1 节 %q, got %q", "08:00-08:45", got)
	}
	custom := &Schedule{
		FirstMonday: time.Date(2026, 9, 7, 0, 0, 0, 0, time.Local),
		NumWeeks:    13,
		SlotTimes:   []string{"08:30-09:10", "09:20-10:00"},
	}
	if got := custom.SlotTimeText(1); got != "08:30-09:10" {
		t.Errorf("覆盖后第 1 节时间 = %q, want 08:30-09:10", got)
	}
	if got := custom.SlotTimeText(3); got != "" {
		t.Errorf("超出时间表长度的节次应返回空串, got %q", got)
	}
}

// TestFormatHMMRange 验证 HHMM 时刻格式化为 "hh:mm-hh:mm"。
func TestFormatHMMRange(t *testing.T) {
	got, err := formatHMMRange(800, 845)
	if err != nil || got != "08:00-08:45" {
		t.Errorf("formatHMMRange(800,845) = %q, %v", got, err)
	}
	got, err = formatHMMRange(1900, 1945)
	if err != nil || got != "19:00-19:45" {
		t.Errorf("formatHMMRange(1900,1945) = %q, %v", got, err)
	}
	if _, err := formatHMMRange(1290, 1300); err == nil {
		t.Error("非法时刻(分≥60)应报错")
	}
}

// TestInferFirstMonday 验证按 rwList 首次上课日期(SCSKRQ)推算第一周周一：
// 样例首条 航天(D151052023)：SCSKRQ=2026-10-16(第6周周五)
// → 所在周周一 10-12 往前推 5 周 → 2026-09-07。
func TestInferFirstMonday(t *testing.T) {
	data := loadXskbSample(t)
	var env xskbEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatal(err)
	}
	got := InferFirstMonday(env.RWList, time.Date(2026, 9, 6, 0, 0, 0, 0, time.Local))
	want := time.Date(2026, 9, 7, 0, 0, 0, 0, time.Local)
	if !got.Equal(want) {
		t.Errorf("InferFirstMonday = %v, want %v", got, want)
	}
	// 空课程列表 → 当年 1 月 1 日
	got2 := InferFirstMonday(nil, time.Date(2026, 6, 15, 0, 0, 0, 0, time.Local))
	want2 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)
	if !got2.Equal(want2) {
		t.Errorf("空列表 InferFirstMonday = %v, want %v", got2, want2)
	}
	// SCSKRQ 全缺失 → 也回退 1 月 1 日
	bad := []XskbRWRecord{{KCDM: "A", PKSJDD: "1-2周 星期一[1-2节]J1"}}
	got3 := InferFirstMonday(bad, time.Date(2026, 6, 15, 0, 0, 0, 0, time.Local))
	if !got3.Equal(want2) {
		t.Errorf("无有效 SCSKRQ 时 = %v, want %v", got3, want2)
	}
}

// TestParseXskbAutoFirstMonday 验证不传 FirstMonday 时解析自动推算。
func TestParseXskbAutoFirstMonday(t *testing.T) {
	data := loadXskbSample(t)
	s, err := ParseXskb(data, ParseOptions{}) // FirstMonday 零值
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 9, 7, 0, 0, 0, 0, time.Local)
	if !s.FirstMonday.Equal(want) {
		t.Errorf("自动推断 FirstMonday = %v, want %v", s.FirstMonday, want)
	}
}
