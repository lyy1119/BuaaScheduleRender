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
//   - 列宽按比例分配（table-layout:fixed），表格宽度恒等于容器，不产生横向溢出，
//     右侧课程信息列宽度受容器约束，内容超出可用宽度时自动换行；
//   - 整个课表包在 div.sheet 中；screen 上容器最大宽度由视口高度与宽高比
//     1.414:1（A4 纸张长宽比 √2）推出（Portrait=true 时翻转成 1:1.414 竖版），
//     打印方向由 @page 按同一参数输出 A4 横/纵。
const (
	cssThin   = "1px solid #000"
	cssStrong = "2px solid #000"
)

// PageRatio 是宽高比的基准（A4 纸张长宽比 √2 ≈ 1.414）。
const PageRatio = 1.414

// RenderOptions 控制渲染版式。
type RenderOptions struct {
	// Portrait 控制宽高比是否翻转：false（默认）横向，宽:高 = 1.414:1；
	// true 竖版，宽:高 = 1:1.414，用于竖着打印（打印 @page 同步 A4 纵向）。
	Portrait bool
}

// 计算课程单元格宽度
// 星期列、周次节次列、节次表的节次列按照CellWidth的1/2绘制
// 单元格大小为 百分比
func (s *Schedule) calCellWidthHeight() (float64, float64) {
	return 5.6, 2
}

// 计算合适的字体大小
// 字体大小为 mm
// 字体大小应根据格子的宽和高计算得到
func (s *Schedule) calFontSize() float64 {
	return 2.5
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
// 布局要点：
//  1. 整个课表包在 <div class="sheet"> 中；不使用任何内部滚动条与裁切。
//     表格 table-layout:fixed、width:100%，其宽度恒等于容器宽度，
//     因此整表（含右侧课程信息列）始终在容器内完整显示、无横向溢出；
//  2. 屏幕预览时容器最大宽度 = min(100%, 视口高度 × 宽高比 1.414)，
//     Portrait=true 时比值翻转（竖版更窄）；打印时 @page 为 A4 横/纵；
//  3. 右侧课程信息列宽度受容器约束：内容超出该列可用宽度时自动换行，
//     不会撑宽表格；主网格列（周次、节次等）因 fixed 布局保持等比列宽。
func (s *Schedule) RenderHTML(w io.Writer, opts RenderOptions) error {
	fontSize := s.calFontSize()
	cellWidth, cellHeight := s.calCellWidthHeight()
	if err := s.Validate(); err != nil {
		return fmt.Errorf("渲染被拒绝：课表数据不合法: %w", err)
	}
	esc := func(v string) string { return template.HTMLEscapeString(v) }

	orient := "横向"

	var b strings.Builder
	b.WriteString("<!DOCTYPE html>\n<html lang=\"zh-CN\">\n<head>\n<meta charset=\"UTF-8\">\n")
	fmt.Fprintf(&b, "<title>%s</title>\n", esc(s.Title))
	b.WriteString("<style>\n")
	// @page 必须位于样式顶层（不能嵌在 @media print 内），按方向动态输出
	// fmt.Fprintf(&b, "  /* 打印纸张方向：%s */\n  %s\n", esc(orient), opts.pageCSS())
	b.WriteString(`  .page { width: 297mm; height: 210mm; margin: 10mm auto; padding: 6mm; overflow:hidden; background: white; }
  html, body { margin: 0; padding: 0; }`)
	fmt.Fprintf(&b, "body { background: #eeeeee; font-family: 'Microsoft YaHei', Arial, sans-serif; color: #000; font-size: %.1fmm;}", fontSize)
	b.WriteString(` h1 { font-size: 18px; text-align: center; margin: 0 0 8px; }
  h2.info-title { font-size: 12px; margin: 0 0 6px; text-align: center; }
  table { border-collapse: collapse; }
  /* 表格宽度恒等于容器：固定布局 + 100% 宽度 → 整表完整显示，绝不横向溢出 */
  table#main { width: 100%; table-layout: fixed; border: ` + cssStrong + `; }
  table#main th, table#main td { border: ` + cssThin + `; padding: 1px 3px; text-align: center; }
  thead { display: table-header-group; }
  thead th { background: #f0f0f0; font-weight: bold; white-space: nowrap; }
  thead tr:nth-child(2) th { border-bottom: ` + cssStrong + `; }
  td.weekday { background: #f0f0f0; font-weight: bold; white-space: nowrap; }
  td.time { white-space: nowrap; }
  td.ps { border-left: ` + cssStrong + `; white-space: nowrap; } /* 时间表/信息表与课程格的竖向粗分隔 */
  td.course { white-space: nowrap; overflow: hidden; }             /* 课程格不折行 */
  td.course.star { color: #555; }
  /* 右侧课程信息列：宽度=剩余可用宽度，内容超宽自动换行 */
  td.info-cell { border-left: ` + cssStrong + `; text-align: left; vertical-align: top; white-space: normal; word-break: break-word; overflow-wrap: break-word; }
  div.info-wrap { padding: 2px 6px 2px 2px; }
  p.ci { margin: 0 0 6px; text-align: left; white-space: normal; word-break: break-word; }
  span.ci-no { font-weight: bold; }
  span.ci-name { font-weight: bold; }
  .ci-line { color: #333; }
  .foot, .tip { text-align: center; margin: 4px 0 0; }
</style>`)
	b.WriteString("</head>\n<body>\n")
	// ---------- 比例容器：包住整份课表 ----------
	b.WriteString("<div class=\"page\">\n")
	fmt.Fprintf(&b, "<h1>%s</h1>\n", esc(s.Title))

	// ---------- 主课表 ----------
	// 行宽限制
	b.WriteString("<table id=\"main\">\n<colgroup>")
	// mainColPcts 主表列宽分配（百分比，合计 100%）：A 星期 / B 节次 较窄；
	// 13 个周次列等宽（锚定"12月31日"日期单行所需宽度）；P/Q 为右侧
	// "节次-时间表/课程信息"列——周一区段显示节次与时间，周二~周日该两列合并
	// 放置课程信息表，宽度 = 剩余百分比，随容器伸缩并在超宽时自动换行。
	var mainColPcts = []float64{
		cellWidth / 2, cellWidth / 2, // A 星期, B 节次
		5.6, 5.6, 5.6, 5.6, 5.6, 5.6, 5.6, 5.6, 5.6, 5.6, 5.6, 5.6, 5.6, // 周次 ×13
		3.0, 20.0, // P 节次, Q 时间/课程信息
	}
	for _, p := range mainColPcts {
		fmt.Fprintf(&b, "<col style=\"width:%.4g%%\">", p)
	}
	b.WriteString("</colgroup>\n<thead>\n<tr>")

	// 表头第 1 行：星期(A, 跨 2 行) | 周次(B1) | 周次编号 1..N(C1..) | 节次-时间表(P1:Q1)
	b.WriteString("<th rowspan=\"2\">星期</th><th>周次</th>")
	for w := 1; w <= s.NumWeeks; w++ {
		fmt.Fprintf(&b, "<th>%d</th>", w)
	}
	b.WriteString("<th colspan=\"2\">节次-时间表</th>")
	b.WriteString("</tr>\n<tr>")
	// 表头第 2 行：节次(B2) | 每周一日期(C2..，强制单行) | 节次(P2)/时间(Q2)
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
				// 周二~周日右侧 P/Q 区域：合并大格，内嵌"课程信息表"（宽度受容器约束，超宽自动换行）
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
						// 完整课程名 | 教师 | 教室 显示在同一行，超出列宽自动换行
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

	fmt.Fprintf(&b, "<p class=\"foot\">第 1 周周一起始日期：%s，共 %d 周 · 版面：A4 %s（宽高比 %.3f:1）</p>\n",
		esc(s.FirstMonday.Format("2006年1月2日")), s.NumWeeks, orient, PageRatio)
	b.WriteString("<p class=\"tip\">打印提示：浏览器中按 Ctrl+P，请选择 A4 纸张（方向已按版面设置）。</p>\n")
	b.WriteString("</div>\n") // .sheet
	b.WriteString("</body>\n</html>\n")

	_, err := io.WriteString(w, b.String())
	return err
}
