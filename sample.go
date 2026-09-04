package schedule

import "time"

// NewSampleSchedule 构造与《课程表填写示例.xlsx》完全一致的示例课表
// （7 门课程元素、共 121 个非空格子），便于对照验证网页渲染与 Excel 模板一致。
//
// 周掩码语义（w 从 1 起，第 w 周 = 第 w-1 位）：
//
//	第 1-13 周全上        ⇔ 0b0001_1111_1111_1111 (0x1FFF)
//	第 2-11 周            ⇔ 0b0000_0111_1111_1110 (0x07FE)
//	单周 1,3,5,…,13       ⇔ 0b0101_0101_0101_0101 (0x5555)
//	第 3-12 周            ⇔ 0b0000_1111_1111_1100 (0x0FFC)
//	仅第 7 周             ⇔ 0b0000_0000_0100_0000 (0x0040)
//
// 若某几周因节假日停课（例如"第 1-3 周有、第 4 周放假、第 5 周恢复"），
// 只需把对应位去掉：NewWeekMask(1, 2, 3, 5, ...)。
func NewSampleSchedule() *Schedule {
	return &Schedule{
		Title:       "2026-2027学年第一学期课表（示例）",
		FirstMonday: time.Date(2026, time.September, 7, 0, 0, 0, 0, time.Local), // 第 1 周周一 = 9月7日
		NumWeeks:    13,
		Courses: []Course{
			{
				Name: "高等数学A(上)", Weekday: Monday, StartSlot: 1, EndSlot: 2,
				WeekMask: NewWeekMask(1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13), // 每周一 1-2 节
				Location: "教3-105",
			},
			{
				Name: "线性代数", Weekday: Tuesday, StartSlot: 6, EndSlot: 7,
				WeekMask: NewWeekMask(2, 3, 4, 5, 6, 7, 8, 9, 10, 11), // 第 2-11 周
				Location: "主M-201",
			},
			{
				Name: "大学英语(二)", Weekday: Wednesday, StartSlot: 1, EndSlot: 2,
				WeekMask: NewWeekMask(1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13),
				Location: "J3-210",
			},
			{
				Name: "形势与政策", Weekday: Wednesday, StartSlot: 11, EndSlot: 12,
				WeekMask: NewWeekMask(7), // 仅第 7 周一次的晚间大课
				Location: "教1-401",
			},
			{
				Name: "体育(乒乓球)", Weekday: Thursday, StartSlot: 5, EndSlot: 5,
				WeekMask: NewWeekMask(1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13), // 单节
				Location: "田径场",
			},
			{
				Name: "大学物理(上)", Weekday: Friday, StartSlot: 3, EndSlot: 4,
				WeekMask: NewWeekMask(1, 3, 5, 7, 9, 11, 13), // 单周上课
				Location: "教5-101",
			},
			{
				Name: "Python程序设计(选修)", Weekday: Saturday, StartSlot: 1, EndSlot: 2,
				WeekMask: NewWeekMask(3, 4, 5, 6, 7, 8, 9, 10, 11, 12), // 第 3-12 周（周末课）
				Location: "机房3-301",
			},
		},
	}
}
