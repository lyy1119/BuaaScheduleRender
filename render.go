package schedule

import (
	"fmt"
	"html/template"
	"io"
	"strings"
)

// 版式与框线规则（不追求与 Excel 逐磅一致，重在层次对比）：
//   - 主表外框 2px 粗线；表头(周次/日期行)下沿 2px 粗线；
//   - 网格内部一律 1px 细线；课程格区域与右侧"节次-时间表/课程信息表"之间
//     的竖向分隔用 2px 粗线；右侧信息区内部不画网格线；
//   - 列宽由"最长日期"（月、日双位数，如 12月31日）锚定，日期表头强制单行，
//     课程名按同一列宽预算严格截断（CellNameBudget），保证课程格也不折行。
const (
	cssThin   = "1px solid #000"
	cssStrong = "2px solid #000"
	// 各周次列宽度(px)：需容纳 DateMaxText("12月31日") 单行显示。
	weekColWidthPx = 62
)

// cellInfo 返回某一（周、星期、节次）格子命中的元素及其显示文本；无课则空。
// 文本规则：
//
//	起始节次格 → "{起始节次} {FitName(课程名)}"（课名按列宽预算严格截断）
//	连堂后续节次格 → "*"
func (s *Schedule) cellInfo(week, weekday, slot int) (string, *CourseElement) {
	for i := range s.Elements {
		e := &s.Elements[i]
		if e.Weekday != weekday || !e.HasWeek(week) || slot < e.StartSlot || slot > e.EndSlot {
			continue
		}
		if slot == e.StartSlot {
			return fmt.Sprintf("%d %s", slot, FitName(e.Name, CellNameBudget)), e
		}
		return "*", e
	}
	return "", nil
}

// CellText 是 cellInfo 的公开文本版。
func (s *Schedule) CellText(week, weekday, slot int) string {
	t, _ := s.cellInfo(week, weekday, slot)
	return t
}

// RenderHTML 把课表渲染成一个完整、自包含（内嵌 CSS）、零脚本的静态 HTML 文档。
// 页面结构：
//
//	① 主课表网格（与《空课程表示例.xlsx》一致）：A 星期 | B 节次 | 周次课程格，
//	   右侧 P/Q 为"节次-时间表"（仅周一区段出现）。
//	② 周二~周日右侧的 P17:Q100 区域放置"课程信息表"：课程按 CourseID 字典序
//	   排列并显示 1、2、3… 序号，每条只含完整课程名/教师/教室（不含上课时间，
//	   课名记录的就是全名，不做另起别名式的简称）。
func (s *Schedule) RenderHTML(w io.Writer) error {
	if err := s.Validate(); err != nil {
		return fmt.Errorf("渲染被拒绝：课表数据不合法: %w", err)
	}
	esc := func(v string) string { return template.HTMLEscapeString(v) }

	var b strings.Builder
	b.WriteString("<!DOCTYPE html>\n<html lang=\"zh-CN\">\n<head>\n<meta charset=\"UTF-8\">\n")
	fmt.Fprintf(&b, "<title>%s</title>\n", esc(s.Title))
	b.WriteString(`<style>
  body { font-family: "Microsoft YaHei", "SimSun", "PingFang SC", sans-serif; margin: 16px; color: #000; }
  h1 { font-size: 20px; text-align: center; margin: 0 0 10px; }
  h2.info-title { font-size: 13px; margin: 2px 0 6px; text-align: center; }
  table { border-collapse: collapse; font-size: 11px; }
  table#main { margin: 0 auto; border: ` + cssStrong + `; }
  table#main th, table#main td { border: ` + cssThin + `; padding: 1px 3px; text-align: center; white-space: nowrap; }
  thead { display: table-header-group; }
  thead th { background: #f0f0f0; font-weight: bold; }
  thead tr:nth-child(2) th { border-bottom: ` + cssStrong + `; }
  td.weekday { background: #f0f0f0; font-weight: bold; min-width: 22px; }
  td.time { font-size: 10px; }
  td.ps { border-left: ` + cssStrong + `; }   /* 时间表/信息表与课程格的竖向粗分隔 */
  td.course { overflow: hidden; }              /* 课程格不折行，超出被列宽裁切 */
  td.course.star { color: #555; }
  td.info-cell { border-left: ` + cssStrong + `; text-align: left; vertical-align: top; white-space: normal; }
  div.info-wrap { padding: 2px 8px 2px 2px; }
  p.ci { margin: 0 0 10px; font-size: 11px; text-align: left; }
  span.ci-no { font-weight: bold; }
  span.ci-name { font-weight: bold; }
  .ci-line { color: #333; }
  .foot, .tip { text-align: center; font-size: 10px; margin-top: 6px; }
  @media print {
    body { margin: 0; }
    @page { size: A4 landscape; margin: 8mm; }
    tr { page-break-inside: avoid; }
    table#main { font-size: 8.5px; }
  }
</style>`)
	b.WriteString("</head>\n<body>\n")
	fmt.Fprintf(&b, "<h1>%s</h1>\n", esc(s.Title))

	// ---------- 主课表 ----------
	b.WriteString("<table id=\"main\">\n<colgroup>")
	// A..Q：A 星期 | B 节次 | C.. 周次（13 列等宽，锚定最长日期）| P 节次 | Q 时间
	b.WriteString("<col style=\"width:24px\"><col style=\"width:20px\">")
	for w := 1; w <= s.NumWeeks; w++ {
		fmt.Fprintf(&b, "<col style=\"width:%dpx\">", weekColWidthPx)
	}
	b.WriteString("<col style=\"width:24px\"><col style=\"width:88px\">")
	b.WriteString("</colgroup>\n<thead>\n<tr>")
	// 表头第 1 行：星期(A, 跨 2 行) | 周次(B1) | 周次编号 1..13(C1..O1) | 节次-时间表(P1:Q1)
	b.WriteString("<th rowspan=\"2\">星期</th><th>周次</th>")
	for w := 1; w <= s.NumWeeks; w++ {
		fmt.Fprintf(&b, "<th>%d</th>", w)
	}
	b.WriteString("<th colspan=\"2\">节次-时间表</th>")
	b.WriteString("</tr>\n<tr>")
	// 表头第 2 行：节次(B2) | 每周一日期(C2..O2，依次补齐、强制单行) | 节次(P2)/时间(Q2)
	b.WriteString("<th>节次</th>")
	for w := 1; w <= s.NumWeeks; w++ {
		fmt.Fprintf(&b, "<th>%s</th>", esc(s.WeekStart(w).Format("1月2日")))
	}
	b.WriteString("<th class=\"ps\">节次</th><th class=\"ps time\">时间</th>")
	b.WriteString("</tr>\n</thead>\n<tbody>\n")

	for day := Monday; day <= Sunday; day++ {
		for slot := 1; slot <= SlotsPerDay; slot++ {
			b.WriteString("<tr>")
			if slot == 1 {
				fmt.Fprintf(&b, "<td class=\"weekday\" rowspan=\"%d\">%s</td>", SlotsPerDay, weekdayNames[day])
			}
			fmt.Fprintf(&b, "<td>%d</td>", slot)
			for w := 1; w <= s.NumWeeks; w++ {
				text, el := s.cellInfo(w, day, slot)
				switch {
				case text == "":
					b.WriteString("<td class=\"course\"></td>")
				case text == "*":
					b.WriteString("<td class=\"course star\">*</td>")
				default:
					title := el.Name
					if el.Teacher != "" {
						title += "｜" + el.Teacher
					}
					if el.Location != "" {
						title += "｜" + el.Location
					}
					fmt.Fprintf(&b, "<td class=\"course\" title=\"%s\">%s</td>", esc(title), esc(text))
				}
			}
			switch {
			case day == Monday: // 周一区段：右侧显示节次-时间表
				fmt.Fprintf(&b, "<td class=\"ps\">%d</td><td class=\"ps time\">%s</td>", slot, SlotTimes[slot-1])
			case day == Tuesday && slot == 1:
				// 周二~周日右侧 P17:Q100 区域：合并大格，内嵌"课程信息表"
				fmt.Fprintf(&b, "<td class=\"info-cell\" colspan=\"2\" rowspan=\"%d\">", 6*SlotsPerDay)
				b.WriteString("<div class=\"info-wrap\">")
				b.WriteString("<h2 class=\"info-title\">课程信息</h2>")
				infos := s.CourseInfos() // 已按 CourseID 字典序排序
				if len(infos) == 0 {
					b.WriteString("<p class=\"ci\">（本学期无课程）</p>")
				} else {
					for i := range infos {
						ci := &infos[i]
						// 每条课程信息用 <p>（左对齐）；不使用 <ol>/<li>，
						// 序号为手写展示序号，排序依据 CourseID，不改动数据
						b.WriteString("<p class=\"ci\">")
						fmt.Fprintf(&b, "<span class=\"ci-no\">%d.</span> <span class=\"ci-name\">%s</span>",
							i+1, esc(ci.Name))
						if len(ci.Teachers) > 0 {
							fmt.Fprintf(&b, "<br><span class=\"ci-line\">教师：%s</span>",
								esc(strings.Join(ci.Teachers, "、")))
						}
						if len(ci.Locations) > 0 {
							fmt.Fprintf(&b, "<br><span class=\"ci-line\">教室：%s</span>",
								esc(strings.Join(ci.Locations, "、")))
						}
						b.WriteString("</p>")
					}
				}
				b.WriteString("</div></td>")
			default: // 周二~周日的其余行：P/Q 已被上方信息表大格覆盖，无需输出
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
