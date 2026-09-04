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
//     课程名按同一列宽预算严格截断（CellNameBudget），保证课程格也不折行；
//   - 整个课表包在一个 div.sheet 中，屏幕与打印宽高比锁定为 1.414:1
//     （A4 纸张长宽比 √2）；Portrait=true 时翻转成 1:1.414（竖版打印）。
const (
	cssThin   = "1px solid #000"
	cssStrong = "2px solid #000"
	// 各周次列宽度(px)：需容纳 DateMaxText("12月31日") 单行显示。
	weekColWidthPx = 62
)

// PageRatio 是宽高比的基准（A4 纸张长宽比 √2 ≈ 1.414）。
const PageRatio = 1.414

// RenderOptions 控制渲染版式。
type RenderOptions struct {
	// Portrait 控制宽高比是否翻转：false（默认）横向，宽:高 = 1.414:1；
	// true 竖版，宽:高 = 1:1.414，用于竖着打印。
	Portrait bool
}

// sheetClass 根据方向返回 div.sheet 使用的 class。
func (o RenderOptions) sheetClass() string {
	if o.Portrait {
		return "sheet portrait"
	}
	return "sheet landscape"
}

// pageCSS 根据方向返回 @page 规则（A4 横/纵）。
func (o RenderOptions) pageCSS() string {
	if o.Portrait {
		return "@page { size: A4 portrait; margin: 8mm; }"
	}
	return "@page { size: A4 landscape; margin: 8mm; }"
}

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
//
// 布局说明：
//  1. 整个课表（标题、主表、页脚）包在一个 <div class="sheet"> 中：
//     - 默认横向：aspect-ratio 1.414 / 1（宽 : 高）；
//     - opts.Portrait=true 时翻转：aspect-ratio 1 / 1.414（竖版打印）；
//     - 屏幕上 .sheet 的宽度以视口高度为基准自适应，整体保持比例；
//     - 表格不脱离容器自由变大：主表 max-width:100%，课程信息等可换行内容
//     在超出可用宽度时自动换行；实在放不下的部分在 .viewport 中滚动查看，
//     打印时取消滚动与比例裁剪，交由 @page(A4 横/纵) 分页。
//  2. 主表网格（与《空课程表示例.xlsx》一致）：A 星期 | B 节次 | 周次课程格，
//     右侧 P/Q 为"节次-时间表"（仅周一区段出现）。
//  3. 周二~周日右侧的 P17:Q100 区域放置"课程信息表"：课程按 CourseID 字典序
//     排列并显示 1、2、3… 序号，每条为同一行展示：完整课程名 | 教师 | 教室
//     （多教师/教室以"、"合并，长度超限自动换行）。
func (s *Schedule) RenderHTML(w io.Writer, opts RenderOptions) error {
	if err := s.Validate(); err != nil {
		return fmt.Errorf("渲染被拒绝：课表数据不合法: %w", err)
	}
	esc := func(v string) string { return template.HTMLEscapeString(v) }

	orient := "横向"
	if opts.Portrait {
		orient = "纵向"
	}

	var b strings.Builder
	b.WriteString("<!DOCTYPE html>\n<html lang=\"zh-CN\">\n<head>\n<meta charset=\"UTF-8\">\n")
	fmt.Fprintf(&b, "<title>%s</title>\n", esc(s.Title))
	b.WriteString("<style>\n")
	// @page 必须位于样式顶层（不能嵌在 @media print 内），按方向动态输出
	fmt.Fprintf(&b, "  /* 打印纸张方向：%s */\n  %s\n", esc(orient), opts.pageCSS())
	b.WriteString(`  html, body { margin: 0; padding: 0; }
  body { background: #f2f2f2; font-family: "Microsoft YaHei", "SimSun", "PingFang SC", sans-serif; color: #000; }
  /* 比例容器：包住整份课表。横向 1.414:1，竖向(portrait)翻转 1:1.414 */
  div.sheet {
    --ratio: 1.414;
    margin: 10px auto;
    width: 100%;
    max-width: calc((100vh - 20px) * var(--ratio)); /* 屏幕：宽随视口高度缩放，保持比例 */
    aspect-ratio: var(--ratio) / 1;
    background: #fff;
    box-sizing: border-box;
    border: 1px solid #bbb;
    padding: 8px 14px;
    overflow: hidden;
  }
  div.sheet.portrait { aspect-ratio: 1 / var(--ratio); max-width: calc((100vh - 20px) / var(--ratio)); }
  div.viewport { width: 100%; max-height: calc(100vh * 0.82); overflow: auto; } /* 超出比例时滚动查看 */
  h1 { font-size: 18px; text-align: center; margin: 0 0 8px; }
  h2.info-title { font-size: 12px; margin: 2px 0 6px; text-align: center; }
  table { border-collapse: collapse; font-size: 11px; }
  table#main { margin: 0 auto; max-width: 100%; border: ` + cssStrong + `; }
  table#main th, table#main td { border: ` + cssThin + `; padding: 1px 3px; text-align: center; }
  thead { display: table-header-group; }
  thead th { background: #f0f0f0; font-weight: bold; white-space: nowrap; }
  thead tr:nth-child(2) th { border-bottom: ` + cssStrong + `; }
  td.weekday { background: #f0f0f0; font-weight: bold; min-width: 22px; white-space: nowrap; }
  td.time { font-size: 10px; white-space: nowrap; }
  td.ps { border-left: ` + cssStrong + `; white-space: nowrap; } /* 时间表/信息表与课程格的竖向粗分隔 */
  td.course { white-space: nowrap; overflow: hidden; }             /* 课程格不折行，超出被列宽裁切 */
  td.course.star { color: #555; }
  td.info-cell { border-left: ` + cssStrong + `; text-align: left; vertical-align: top; white-space: normal; word-break: break-all; }
  div.info-wrap { padding: 2px 6px 2px 2px; }
  p.ci { margin: 0 0 6px; font-size: 11px; text-align: left; white-space: normal; }
  span.ci-no { font-weight: bold; }
  span.ci-name { font-weight: bold; }
  .ci-line { color: #333; }
  .foot, .tip { text-align: center; font-size: 10px; margin: 4px 0 0; }
  @media print {
    body { background: #fff; }
    div.sheet { max-width: none; width: auto; margin: 0; border: none; padding: 0; aspect-ratio: auto; overflow: visible; }
    div.viewport { max-height: none; overflow: visible; }
    table#main { font-size: 8.5px; }
    tr { page-break-inside: avoid; }
  }
</style>`)
	b.WriteString("</head>\n<body>\n")
	// ---------- 比例容器：包住整份课表 ----------
	fmt.Fprintf(&b, "<div class=\"%s\">\n", esc(opts.sheetClass()))
	fmt.Fprintf(&b, "<h1>%s</h1>\n", esc(s.Title))

	// ---------- 主课表 ----------
	b.WriteString("<div class=\"viewport\">\n")
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
						// 每条课程信息为一个 <p>（左对齐），不使用 <ol>/<li> 列表；
						// 完整课程名 | 教师 | 教室 显示在同一行，超出宽度自动换行
						b.WriteString("<p class=\"ci\">")
						fmt.Fprintf(&b, "<span class=\"ci-no\">%d.</span> <span class=\"ci-name\">%s</span>",
							i+1, esc(ci.Name))
						if len(ci.Teachers) > 0 {
							fmt.Fprintf(&b, "<span class=\"ci-line\">| %s</span>",
								esc(strings.Join(ci.Teachers, "、")))
						}
						if len(ci.Locations) > 0 {
							fmt.Fprintf(&b, "<span class=\"ci-line\">| %s</span>",
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
	b.WriteString("</div>\n") // .viewport

	fmt.Fprintf(&b, "<p class=\"foot\">第 1 周周一起始日期：%s，共 %d 周 · 版面：A4 %s（宽高比 %.3f:1）</p>\n",
		esc(s.FirstMonday.Format("2006年1月2日")), s.NumWeeks, orient, PageRatio)
	b.WriteString("<p class=\"tip\">打印提示：浏览器中按 Ctrl+P，请选择 A4 纸张（比例已按方向设置），缩放可调。</p>\n")
	b.WriteString("</div>\n") // .sheet
	b.WriteString("</body>\n</html>\n")

	_, err := io.WriteString(w, b.String())
	return err
}
