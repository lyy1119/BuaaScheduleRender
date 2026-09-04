package schedule

import (
	"fmt"
	"html/template"
	"io"
	"strings"
)

// RenderOptions 控制渲染细节。
type RenderOptions struct {
	ShowLocation bool // 课程格内是否追加显示上课地点（换行显示）
	ShowTeacher  bool // 课程格内是否追加显示任课教师（换行显示）
}

// cellText 计算某一（周、星期、节次）格子的显示内容：
// 在该 (day, slot) 上课、且位掩码命中第 week 周的课程名称；
// 若 ShowLocation/ShowTeacher 打开则追加地点与教师。无课时返回空串。
func (s *Schedule) cellText(week, weekday, slot int, o RenderOptions) string {
	for i := range s.Courses {
		c := &s.Courses[i]
		if c.Weekday != weekday || slot < c.StartSlot || slot > c.EndSlot || !c.HasWeek(week) {
			continue
		}
		parts := []string{c.Name}
		if o.ShowLocation && c.Location != "" {
			parts = append(parts, c.Location)
		}
		if o.ShowTeacher && c.Teacher != "" {
			parts = append(parts, c.Teacher)
		}
		return strings.Join(parts, "<br>")
	}
	return ""
}

// RenderHTML 把课表渲染成一个完整、自包含（内嵌 CSS）的 HTML 文档写入 w。
// 表格布局与《样本.xlsx》逐格一致：
//
//	第 1 行表头：周次列名 + 第 1..NumWeeks 周的编号；
//	第 2 行表头：星期 / 节次 / 时间 + 每一周对应的【周一日期】（由 FirstMonday 依次推进）；
//	数据区：每天一个 14 行的块（星期列用 rowspan=14 视觉合并，同模板 A 列合并），
//	每行依次是节次、该节时间，以及每个周次列的一个格子。
//	某周停课（掩码该位为 0）时对应格子为空。
func (s *Schedule) RenderHTML(w io.Writer, o RenderOptions) error {
	if err := s.Validate(); err != nil {
		return fmt.Errorf("渲染被拒绝：课表数据不合法: %w", err)
	}
	esc := func(v string) string { return template.HTMLEscapeString(v) }

	var b strings.Builder
	b.WriteString("<!DOCTYPE html>\n<html lang=\"zh-CN\">\n<head>\n<meta charset=\"UTF-8\">\n")
	fmt.Fprintf(&b, "<title>%s</title>\n", esc(s.Title))
	// 样式刻意保持朴素：黑白、细实线边框、清晰可辨；
	// 无任何脚本，纯静态页面，浏览器中直接 Ctrl+P（建议横向）即可打印，
	// @page 默认 A4 横向，打印预览里可切换 A3。
	b.WriteString(`<style>
  body { font-family: "Microsoft YaHei", "SimSun", "PingFang SC", sans-serif; margin: 16px; color: #000; }
  h1 { font-size: 18px; text-align: center; margin: 0 0 10px; }
  table { border-collapse: collapse; margin: 0 auto; font-size: 9px; }
  th, td { border: 1px solid #000; padding: 1px 4px; text-align: center; vertical-align: middle; }
  thead { display: table-header-group; }
  th { background: #eee; font-weight: bold; }
  td.weekday { background: #eee; font-weight: bold; min-width: 18px; }
  td.slot { font-weight: normal; }
  td.time { white-space: nowrap; }
  .foot, .tip { text-align: center; font-size: 10px; margin-top: 6px; }
  @media print {
    body { margin: 0; }
    @page { size: A4 landscape; margin: 8mm; }
    tr { page-break-inside: avoid; }
  }
</style>`)
	b.WriteString("</head>\n<body>\n")
	fmt.Fprintf(&b, "<h1>%s</h1>\n", esc(s.Title))

	b.WriteString("<table>\n<thead>\n<tr>")
	b.WriteString("<th colspan=\"2\"></th><th>周次</th>")
	for w := 1; w <= s.NumWeeks; w++ {
		fmt.Fprintf(&b, "<th>%d</th>", w)
	}
	b.WriteString("</tr>\n<tr>")
	b.WriteString("<th>星期</th><th>节次</th><th>时间</th>")
	for w := 1; w <= s.NumWeeks; w++ {
		fmt.Fprintf(&b, "<th class=\"date\">%s</th>", esc(s.WeekStart(w).Format("1月2日")))
	}
	b.WriteString("</tr>\n</thead>\n<tbody>\n")

	for day := Monday; day <= Sunday; day++ {
		for slot := 1; slot <= SlotsPerDay; slot++ {
			b.WriteString("<tr>")
			if slot == 1 { // 每天第一个节次行输出星期标签，rowspan 视觉合并 14 行
				fmt.Fprintf(&b, "<td class=\"weekday\" rowspan=\"%d\">%s</td>", SlotsPerDay, weekdayNames[day])
			}
			fmt.Fprintf(&b, "<td class=\"slot\">%d</td><td class=\"time\">%s</td>", slot, SlotTimes[slot-1])
			for w := 1; w <= s.NumWeeks; w++ {
				if text := s.cellText(w, day, slot, o); text != "" {
					fmt.Fprintf(&b, "<td class=\"course\">%s</td>", text)
				} else {
					b.WriteString("<td></td>")
				}
			}
			b.WriteString("</tr>\n")
		}
	}
	b.WriteString("</tbody>\n</table>\n")
	fmt.Fprintf(&b, "<p class=\"foot\">第 1 周周一起始日期：%s，共 %d 周</p>\n",
		esc(s.FirstMonday.Format("2006年1月2日")), s.NumWeeks)
	b.WriteString("<p class=\"tip\">打印提示：浏览器中按 Ctrl+P，纸张建议 A4（或 A3）横向，缩放可调。</p>\n")
	b.WriteString("</body>\n</html>\n")

	_, err := io.WriteString(w, b.String())
	return err
}
