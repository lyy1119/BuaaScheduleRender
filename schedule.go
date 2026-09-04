// Package schedule 建模"一个学期的课表"，其行列布局与仓库中《空课程表示例.xlsx》
// 一致：
//
//	表头第 1 行：星期(A) | 周次 | 周次编号 1..N | 节次-时间表(P1:Q1)
//	表头第 2 行：节次 | 每周周一的日期(渲染时依次补齐) | 节次/时间
//	数据区   ：A 列星期(每 14 行合并) | B 列节次 | 周次 1..N 的课程格
//	            P/Q 列 = 节次-时间表，仅在周一区段显示；
//	            周二~周日 P17:Q100 区域渲染"课程信息表"。
//
// 数据模型要点：
//
//  1. 一门"真实课程"（如高等数学）一周可能有多段上课时间（周一 1-2 节、周三 3-4 节），
//     每段上课时间存储为一个 CourseElement（课程元素）。元素共享 CourseID，
//     并携带课程名、教师、教室等信息。注意：真实环境里 CourseID 是任意字符串
//     （如 "MATH101"、"CSE-2026-1"），不是连续数字。
//
//  2. 课程没有单独的"简称"字段：课表格空间有限，显示时按列宽预算对课程名做
//     严格截断（见 FitName / CellNameBudget），数据中记录的始终是完整课程名。
//
//  3. 每段上课时间出现的周用二进制位掩码 WeekMask 记录：
//     第 w 周有课 ⇔ 第 (w-1) 位为 1。第 4 周节假日停课只需让位 3 为 0。
package schedule

import (
	"errors"
	"fmt"
	"sort"
	"time"
)

// 星期常量：1=周一 … 7=周日（与样本 A 列顺序一致）。
const (
	Monday = 1 + iota // 周一 = 1
	Tuesday
	Wednesday
	Thursday
	Friday
	Saturday
	Sunday // 周日 = 7
)

// weekdayNames 将 Weekday 数值映射为中文"一~日"。
var weekdayNames = [8]string{"", "一", "二", "三", "四", "五", "六", "日"}

// SlotsPerDay 每天固定节数。
const SlotsPerDay = 14

// SlotTimes 每节课的标准时间（与样本右侧"节次-时间表"一致）；下标 i 对应第 i+1 节。
var SlotTimes = [SlotsPerDay]string{
	"08:00-08:45", // 第 1 节
	"08:50-09:35", // 第 2 节
	"09:50-10:35", // 第 3 节
	"10:40-11:25", // 第 4 节
	"11:30-12:15", // 第 5 节
	"14:00-14:45", // 第 6 节
	"14:50-15:35", // 第 7 节
	"15:50-16:35", // 第 8 节
	"16:40-17:25", // 第 9 节
	"17:30-18:15", // 第 10 节
	"19:00-19:45", // 第 11 节
	"19:50-20:35", // 第 12 节
	"20:40-21:25", // 第 13 节
	"21:30-22:15", // 第 14 节
}

// 课表格字符预算的推导顺序（先定日期列宽，再限课程名长度）：
//
//	① 表头每周一日期最长为"月日双位数"，即 "12月31日"（6 个字符、视觉最宽）；
//	② 各周次列的宽度按该最长日期设置，保证日期行永远单行、各列等宽；
//	③ 同一列里的课程格内容宽度不能超过日期列宽，因此课程名只能显示一部分：
//	   CellNameBudget = 最长日期的视觉余量（日期含 2 个全角汉字，课程名前缀为
//	   半角节次号+空格，两者视觉宽度接近），课程名最多显示 CellNameBudget 个字符，
//	   其余被严格截断——格子里有节次序号可定位，完整课程名在右侧"课程信息表"中。
const (
	// DateMaxText 是可能出现的日期最长文本（月、日均为两位数），用于锚定列宽。
	DateMaxText = "12月31日"
	// CellNameBudget 课表格中课程名最多显示的字符（rune）数。
	CellNameBudget = 4
)

// FitName 将课程名严格截断为至多 budget 个字符（按 rune，中文/英文一视同仁）。
// 它不做任何"另起别名"式的加工，只是直接截短；超出部分被丢弃，
// 完整名称仍保存在数据中（课程信息表展示完整名称）。
func FitName(name string, budget int) string {
	r := []rune(name)
	if budget <= 0 {
		return ""
	}
	if len(r) <= budget {
		return name
	}
	return string(r[:budget])
}

// CourseElement 是一段"上课时间元素"：同一门真实课程在若干个周的某个星期几、
// 从 StartSlot 到 EndSlot 的连续若干节次上课（1-2 节连堂 = StartSlot 1、EndSlot 2）。
// 同一门课的多段上课时间（周一 1-2 节、周三 3-4 节…）分别存储为不同元素，
// 共享同一个 CourseID；元素冗余携带课程名/教师/教室信息。
type CourseElement struct {
	CourseID  string // 真实课程唯一 ID（任意字符串，非连续数字）
	Name      string // 课程完整名称（数据中记录的就是它；简称仅由渲染时截断派生）
	Teacher   string // 任课教师
	Location  string // 上课地点/教室
	Weekday   int    // 星期几：Monday=1 … Sunday=7
	StartSlot int    // 起始节次：1..SlotsPerDay
	EndSlot   int    // 结束节次：StartSlot..SlotsPerDay（单节课 EndSlot=StartSlot）
	WeekMask  uint32 // 哪些周有课：第 w 周有课 ⇔ 第 (w-1) 位为 1
}

// CourseInfo 是按 CourseID 去重聚合后的一门"真实课程"（课程信息表的一行）。
// 同一 CourseID 的多个元素可能由不同教师授课、或中途变更教师/教室，
// 因此这里把出现的教师与教室分别合并去重（保持首次出现顺序）后展示。
type CourseInfo struct {
	CourseID  string
	Name      string   // 课程名：取同 CourseID 首次出现的名称
	Teachers  []string // 合并去重后的教师列表（可能有多个，如不同老师分段授课/代课）
	Locations []string // 合并去重后的教室列表
}

// Schedule 是一个学期课表：已知第 1 周周一的日期 FirstMonday，
// 第 w 周周一 = FirstMonday + 7×(w-1) 天，从而每一周的日期都可自动推算。
type Schedule struct {
	Title       string
	FirstMonday time.Time // 学期第 1 周周一的日期（样本 9月7号）
	NumWeeks    int       // 学期周数（样本为 13）
	Elements    []CourseElement
}

// NewWeekMask 把周序号列表（从 1 开始）折叠成位掩码。
// NewWeekMask(1, 2, 3, 5) → 第 1,2,3,5 周有课（第 4 周停课）。
func NewWeekMask(weeks ...int) uint32 {
	var m uint32
	for _, w := range weeks {
		if w >= 1 {
			m |= 1 << (w - 1)
		}
	}
	return m
}

// HasWeek 报告该元素在第 w 周是否有课（位掩码判断）。
func (e *CourseElement) HasWeek(w int) bool {
	return w >= 1 && e.WeekMask&(1<<(w-1)) != 0
}

// WeekStart 返回第 w 周周一的日期（w 从 1 开始）。
func (s *Schedule) WeekStart(w int) time.Time {
	return s.FirstMonday.AddDate(0, 0, 7*(w-1))
}

// Validate 检查数据一致性：字段合法性、掩码是否超出学期周数、
// 同一 CourseID 的多个元素信息是否一致，以及不同元素的时间是否冲突
// （同一 周×星期×节次 格子最多只能被一个元素占用）。
func (s *Schedule) Validate() error {
	if s.NumWeeks < 1 {
		return errors.New("schedule: NumWeeks 必须 ≥ 1")
	}
	if s.FirstMonday.IsZero() {
		return errors.New("schedule: 必须设置 FirstMonday（第 1 周周一的日期）")
	}
	seen := map[[3]int]string{} // {周, 星期, 节次} → 元素所属课程 ID（冲突检测）
	for i := range s.Elements {
		e := &s.Elements[i]
		if e.CourseID == "" {
			return fmt.Errorf("课程元素 #%d: CourseID 不能为空", i+1)
		}
		if e.Name == "" {
			return fmt.Errorf("课程元素 #%d (%s): 课程名不能为空", i+1, e.CourseID)
		}
		if e.Weekday < Monday || e.Weekday > Sunday {
			return fmt.Errorf("课程 %q: Weekday=%d 超出 1..7", e.Name, e.Weekday)
		}
		if e.StartSlot < 1 || e.StartSlot > SlotsPerDay {
			return fmt.Errorf("课程 %q: StartSlot=%d 超出 1..%d", e.Name, e.StartSlot, SlotsPerDay)
		}
		if e.EndSlot < e.StartSlot || e.EndSlot > SlotsPerDay {
			return fmt.Errorf("课程 %q: EndSlot=%d 不在 [StartSlot=%d, %d]", e.Name, e.EndSlot, e.StartSlot, SlotsPerDay)
		}
		if e.WeekMask == 0 {
			return fmt.Errorf("课程 %q: WeekMask 为 0（没有任何一周有课）", e.Name)
		}
		if e.WeekMask>>s.NumWeeks != 0 {
			return fmt.Errorf("课程 %q: 周掩码包含了超出学期周数 NumWeeks=%d 的周", e.Name, s.NumWeeks)
		}
		for w := 1; w <= s.NumWeeks; w++ {
			if !e.HasWeek(w) {
				continue
			}
			for slot := e.StartSlot; slot <= e.EndSlot; slot++ {
				key := [3]int{w, e.Weekday, slot}
				if prev, dup := seen[key]; dup {
					return fmt.Errorf("格子冲突：第 %d 周 周%s 第 %d 节 课程 %q 与 %q 时间重叠",
						w, weekdayNames[e.Weekday], slot, prev, e.CourseID)
				}
				seen[key] = e.CourseID
			}
		}
	}
	// 注意：不校验"同一 CourseID 各元素信息一致"——真实场景中同一课程可能由
	// 不同老师在不同时段授课，甚至中途变更教师/教室。右侧展示时的合并去重
	// 由 CourseInfos 完成。
	return nil
}

// CourseInfos 按 CourseID 去重聚合全部真实课程，返回课程信息表的行数据。
// 同一 CourseID 的多个元素如果教师/教室不同（不同老师分段授课、代课或换教室），
// 会被分别合并进 Teachers / Locations（按元素首次出现顺序去重）。
// 由于真实 CourseID 是任意字符串（非连续数字），这里用一个简单、稳定、
// 快速的规则决定课程顺序：按 CourseID 的字符串字典序升序排列；
// 渲染层再按该顺序显示 1、2、3… 展示序号，数据中记录的 CourseID 不受任何影响。
func (s *Schedule) CourseInfos() []CourseInfo {
	idx := map[string]int{}
	var out []CourseInfo
	appendUnique := func(list []string, v string) []string {
		for _, x := range list {
			if x == v {
				return list
			}
		}
		return append(list, v)
	}
	for i := range s.Elements {
		e := &s.Elements[i]
		pos, ok := idx[e.CourseID]
		if !ok {
			pos = len(out)
			idx[e.CourseID] = pos
			out = append(out, CourseInfo{CourseID: e.CourseID, Name: e.Name})
		}
		if e.Teacher != "" {
			out[pos].Teachers = appendUnique(out[pos].Teachers, e.Teacher)
		}
		if e.Location != "" {
			out[pos].Locations = appendUnique(out[pos].Locations, e.Location)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CourseID < out[j].CourseID })
	return out
}
