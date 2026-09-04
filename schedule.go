// Package schedule 建模"一个学期的课表"，其行列布局与仓库中《样本.xlsx》
// 模板一致：
//
//	列 = 周次（第 1..N 周），已知第 1 周周一的日期后可推算出每一周的周一日期；
//	行 = 星期（一~日，每天固定 14 节）× 节次，附每节的标准起止时间。
//	格 = 某一周 · 某一天 · 某一节是否有课。
//
// 课程按"课程元素"建模（一层一层嵌套在学期课表里）：
// 一个元素 = 同一门课在【若干个周】的【某个星期几】的【连续若干节】出现。
// 它出现的周的集合用二进制位掩码 WeekMask 记录：
//
//	第 w 周有课  ⇔  WeekMask 的第 (w-1) 位为 1   （w 从 1 开始）
//
// 位掩码天然表达各种周型：
//
//	1-3 周有课、第 4 周恰逢节假日停课、第 5 周恢复  → 位 0,1,2,4 置 1，位 3 为 0；
//	单周 / 双周                                    → 奇/偶位分别置 1；
//	整学期（模板 13 周）                           → 低 13 位全 1（0x1FFF）。
package schedule

import (
	"errors"
	"fmt"
	"time"
)

// 星期常量：1=周一 … 7=周日（与 Excel 模板 A 列顺序一致）。
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

// SlotsPerDay 每天固定节数（与模板一致）。
const SlotsPerDay = 14

// SlotTimes 每节课的标准时间（与模板 C 列一致）；下标 i 对应第 i+1 节。
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

// Course 是一个"课程元素"：同一门课在若干个周的某个星期几、
// 从 StartSlot 到 EndSlot 的连续若干节次上课（1-2 节连堂即 StartSlot=1, EndSlot=2）。
// 周集合用 WeekMask 位掩码记录；一格课程填格时，凡是掩码命中的周都出现该课程，
// 未被命中的周（例如节假日那一周）该格为空——这正好对应"不是每周都有这门课"。
type Course struct {
	Name      string // 课程名称，必填
	Weekday   int    // 星期几：Monday=1 … Sunday=7，必填
	StartSlot int    // 起始节次：1..SlotsPerDay，必填
	EndSlot   int    // 结束节次：StartSlot..SlotsPerDay，必填（单节课 EndSlot=StartSlot）
	WeekMask  uint32 // 哪些周有课：第 w 周有课 ⇔ 第 (w-1) 位为 1；0 视为无课并报错
	Location  string // 上课地点/教室，可选
	Teacher   string // 任课教师，可选
}

// Schedule 是一个学期课表：已知第 1 周周一的日期 FirstMonday，
// 第 w 周周一 = FirstMonday + 7×(w-1) 天，从而每一周的日期都可自动推算。
// 行/列模板与《样本.xlsx》一致：列数 = NumWeeks，行 = 7 天 × SlotsPerDay 节。
type Schedule struct {
	Title       string    // 课表名称，例如"2026-2027学年第一学期课表"
	FirstMonday time.Time // 学期第 1 周周一的日期（模板中为 9 月 7 号）
	NumWeeks    int       // 学期周数（模板为 13），必须 ≥ 1
	Courses     []Course  // 本学期全部课程元素
}

// NewWeekMask 把周序号列表（从 1 开始）折叠成位掩码：
//
//	NewWeekMask(1, 2, 3, 5) → 第 1,2,3,5 周有课（第 4 周停课）
func NewWeekMask(weeks ...int) uint32 {
	var m uint32
	for _, w := range weeks {
		if w >= 1 {
			m |= 1 << (w - 1)
		}
	}
	return m
}

// HasWeek 报告该课程在第 w 周是否有课（位掩码判断，渲染时即用它逐周探测）。
func (c *Course) HasWeek(w int) bool {
	return w >= 1 && c.WeekMask&(1<<(w-1)) != 0
}

// WeekStart 返回第 w 周周一的日期（w 从 1 开始）。
// 由 FirstMonday 每 7 天推进一次得到，例如第 1 周 9/7、第 2 周 9/14、第 13 周 11/30。
func (s *Schedule) WeekStart(w int) time.Time {
	return s.FirstMonday.AddDate(0, 0, 7*(w-1))
}

// Validate 检查课表数据的一致性，任何问题都会给出带定位信息的错误：
// 学期参数、每门课程的字段合法性、掩码是否超出学期周数，
// 以及最关键的——同一（周、星期、节次）格子是否被多门课占用（冲突）。
// 渲染前应调用它。
func (s *Schedule) Validate() error {
	if s.NumWeeks < 1 {
		return errors.New("schedule: NumWeeks 必须 ≥ 1")
	}
	if s.FirstMonday.IsZero() {
		return errors.New("schedule: 必须设置 FirstMonday（第 1 周周一的日期）")
	}
	seen := map[[3]int]string{} // {周, 星期, 节次} → 课程名
	for i := range s.Courses {
		c := &s.Courses[i]
		if c.Name == "" {
			return fmt.Errorf("课程 #%d: 课程名不能为空", i+1)
		}
		if c.Weekday < Monday || c.Weekday > Sunday {
			return fmt.Errorf("课程 %q: Weekday=%d 超出 1..7", c.Name, c.Weekday)
		}
		if c.StartSlot < 1 || c.StartSlot > SlotsPerDay {
			return fmt.Errorf("课程 %q: StartSlot=%d 超出 1..%d", c.Name, c.StartSlot, SlotsPerDay)
		}
		if c.EndSlot < c.StartSlot || c.EndSlot > SlotsPerDay {
			return fmt.Errorf("课程 %q: EndSlot=%d 不在 [StartSlot=%d, %d]", c.Name, c.EndSlot, c.StartSlot, SlotsPerDay)
		}
		if c.WeekMask == 0 {
			return fmt.Errorf("课程 %q: WeekMask 为 0（没有任何一周有课）", c.Name)
		}
		if c.WeekMask>>s.NumWeeks != 0 {
			return fmt.Errorf("课程 %q: 周掩码包含了超出学期周数 NumWeeks=%d 的周", c.Name, s.NumWeeks)
		}
		for w := 1; w <= s.NumWeeks; w++ {
			if !c.HasWeek(w) {
				continue
			}
			for slot := c.StartSlot; slot <= c.EndSlot; slot++ {
				key := [3]int{w, c.Weekday, slot}
				if prev, dup := seen[key]; dup {
					return fmt.Errorf("格子冲突：第 %d 周 周%s 第 %d 节同时有 %q 与 %q",
						w, weekdayNames[c.Weekday], slot, prev, c.Name)
				}
				seen[key] = c.Name
			}
		}
	}
	return nil
}
