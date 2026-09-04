package schedule

import "time"

// NewSampleSchedule 构造一份示例课表，演示新版模型：
//   - 课程只记录完整名称 Name（没有"另起别名"式简称；课表格中的短文本由渲染时
//     用 FitName 按列宽预算对完整课名严格截断得到）；
//   - 同一门"真实课程"可以拆成多个 CourseElement（高等数学：周一 1-2 节 + 周三
//     3-4 节），元素共享 CourseID；课程信息表按 CourseID 去重，并按 CourseID
//     字典序排列显示 1、2、3… 序号（真实 CourseID 可以是任意字符串，如 M-101）。
func NewSampleSchedule() *Schedule {
	weeks13 := NewWeekMask(1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13)
	return &Schedule{
		Title:       "2026-2027学年第一学期课表（示例）",
		FirstMonday: time.Date(2026, time.September, 7, 0, 0, 0, 0, time.Local), // 第 1 周周一 = 9月7日
		NumWeeks:    13,
		Elements: []CourseElement{
			// 高等数学：同一课程两个时间元素 → 课程信息表只显示一行。
			// 注意：周三的课由另一位老师（李敏）在另一个教室上——同一 CourseID
			// 允许不同教师/教室（如分段授课、代课或换教室），右侧信息表会合并展示。
			{CourseID: "M-101", Name: "高等数学A(上)", Teacher: "王建国",
				Location: "教3-105", Weekday: Monday, StartSlot: 1, EndSlot: 2, WeekMask: weeks13},
			{CourseID: "M-101", Name: "高等数学A(上)", Teacher: "李敏",
				Location: "主M-201", Weekday: Wednesday, StartSlot: 3, EndSlot: 4, WeekMask: weeks13},

			// 线性代数：第 2-11 周（真实 ID 为非数字字符串示例）
			{CourseID: "lin-alg-02", Name: "线性代数", Teacher: "赵明",
				Location: "主M-201", Weekday: Tuesday, StartSlot: 6, EndSlot: 7,
				WeekMask: NewWeekMask(2, 3, 4, 5, 6, 7, 8, 9, 10, 11)},

			// 大学英语(二)：周三 1-2 节 + 周五 5 节 两个时间元素
			{CourseID: "EN-202", Name: "大学英语(二)", Teacher: "周莉",
				Location: "J3-210", Weekday: Wednesday, StartSlot: 1, EndSlot: 2, WeekMask: weeks13},
			{CourseID: "EN-202", Name: "大学英语(二)", Teacher: "周莉",
				Location: "J3-210", Weekday: Friday, StartSlot: 5, EndSlot: 5, WeekMask: weeks13},

			// 形势与政策：仅第 7 周一次的晚间大课
			{CourseID: "P-301", Name: "形势与政策", Teacher: "张老师",
				Location: "教1-401", Weekday: Wednesday, StartSlot: 11, EndSlot: 12, WeekMask: NewWeekMask(7)},

			// 体育(乒乓球)：单节
			{CourseID: "PE-401", Name: "体育(乒乓球)", Teacher: "刘教练",
				Location: "田径场", Weekday: Thursday, StartSlot: 5, EndSlot: 5, WeekMask: weeks13},

			// 大学物理(上)：单周（1,3,5,…,13）
			{CourseID: "P-101", Name: "大学物理(上)", Teacher: "陈平",
				Location: "教5-101", Weekday: Friday, StartSlot: 3, EndSlot: 4,
				WeekMask: NewWeekMask(1, 3, 5, 7, 9, 11, 13)},

			// Python程序设计(选修)：第 3-12 周 周六
			{CourseID: "CS-501", Name: "Python程序设计(选修)", Teacher: "孙老师",
				Location: "机房3-301", Weekday: Saturday, StartSlot: 1, EndSlot: 2,
				WeekMask: NewWeekMask(3, 4, 5, 6, 7, 8, 9, 10, 11, 12)},
		},
	}
}
