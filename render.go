package schedule

import (
	"fmt"
	"html/template"
	"io"
	"strings"
)

// 版式与框线规则：
//   - 页面固定为 A4 纸大小（默认横向 297×210mm；Portrait=true 翻转 210×297mm），
//     内边距 6mm → 内容可用区 = 纸张 − 12mm。整个课表以"单元格"为单位排布：
//     标题占 2 个单元格高度，表格共 14×7+2 = 100 个单元格高度；
//   - 主表外框与表头下沿用 2px 粗线，网格内部 1px 细线，课程格与右侧
//     "节次-时间表/课程信息" 之间的竖向分隔用 2px 粗线；
//   - 文字高度（除标题外）全表一致：由单元格高/宽共同约束、先取 mm 计算
//     再换算百分比（行高、列宽）输出到 CSS。
const (
	cssThin   = "1px solid #000"
	cssStrong = "2px solid #000"

	// 单元格行模型：表格 14 节 × 7 天 + 2 个表头行 = 100 行；标题另占 2 行。
	tableCellRows = SlotsPerDay*7 + 2
	titleCellRows = 1.5
	// 单元格宽度模型：星期列/节次列各 0.5 格，周次列每列 1 格，
	// 右侧节次-时间表的节次列 0.5 格、时间列最少 2 格（其余空白自动补全）。
	cellWDayCol   = 0.5 // 星期列（A）
	cellWSlotCol  = 0.5 // 节次/周次列（B）
	cellWWeekCol  = 1.0 // 周次列
	cellWPSlotCol = 0.5 // 右侧节次列（P）
	cellWTimeCol  = 2.0 // 右侧时间列（Q）的最小宽度
	// 约束：周次列恰好容纳 4.5 个全角字符宽（如"12月29日"）；一个单元格容纳 1.5 个字符高。
	fullCharsInWeekCol = 4.5
	charHeightsInCell  = 1.5
)

// RenderOptions 控制渲染版式。
type RenderOptions struct {
	// Portrait 为 true 时页面翻转为 A4 纵向（210×297mm），用于竖着打印。
	Portrait bool
}

// layout 是一份 A4 页面全部版式参数（同时由同一套计算得出，先 mm 后换算百分比）。
type layout struct {
	portrait bool

	pageWMM, pageHMM float64 // 纸张尺寸
	usableWMM        float64 // 内容可用宽度 = 纸宽 - 2×6mm
	usableHMM        float64 // 内容可用高度 = 纸高 - 2×6mm

	cellHMM float64 // 单元格高度（mm）
	cellWMM float64 // 基准单元格宽度（mm，= 周次列宽度）
	fontMM  float64 // 正文字号（mm，除标题外全表一致）

	titleHMM    float64 // 标题占用高度（2 个单元格高）
	titleFontMM float64 // 标题字号（可不同于正文）

	colMM  []float64 // 各列宽度（mm，长度 = NumWeeks+4）
	colPct []float64 // 各列宽度换算为占可用宽度的百分比（合计 ≈ 100%）

	lineHMM float64 // 行高（mm，= 单元格高度，供信息区文本行使用）
}

// 纸张与内边距（毫米）。
const (
	pagePadMM = 6.0
	pageGapMM = 2 * pagePadMM
)

// calcLayout 合并计算"行高、列宽、文字大小"（用户要求同时得出，故为单一函数）：
//
//	高度方向：cellH = 可用高 / (100 表格行 + 2 标题行)
//	           字体上限 fs_h = cellH / 1.5（一格容纳 1.5 个字符高）
//	宽度方向：先假设右侧取最小值，把可用宽均分给各列单位：
//	           W0 = 可用宽 / (NumWeeks + 3.5)     （0.5+0.5+N+0.5+2 = N+3.5）
//	           字体上限 fs_w = W0 / 4.5（周列恰好容纳 4.5 个全角字符）
//	字号 fs = min(fs_h, fs_w)
//	   · 若高度更紧张（fs=fs_h）：用 fs 反推基准列宽 cellW = fs×4.5，
//	     表格（除右侧外）收窄，剩余宽度被右侧"时间/课程信息"列自动补全；
//	   · 若宽度更紧张（fs=fs_w）：列宽维持 W0，右侧取最小宽度 2×cellW。
//	输出同时给出 mm 值与换算好的百分比（列宽占可用宽、行高 line-height 相对字号）。
func (s *Schedule) calcLayout(portrait bool) layout {
	ly := layout{portrait: portrait}
	if portrait {
		ly.pageWMM, ly.pageHMM = 210, 297
	} else {
		ly.pageWMM, ly.pageHMM = 297, 210
	}
	ly.usableWMM = ly.pageWMM - pageGapMM
	ly.usableHMM = ly.pageHMM - pageGapMM

	totalUnits := tableCellRows + titleCellRows
	ly.cellHMM = ly.usableHMM / float64(totalUnits)
	ly.titleHMM = float64(titleCellRows) * ly.cellHMM

	// 宽度方向（含右侧最小 0.5+2）
	units := cellWDayCol + cellWSlotCol + float64(s.NumWeeks)*cellWWeekCol + cellWPSlotCol + cellWTimeCol
	w0 := ly.usableWMM / units

	fsH := ly.cellHMM / charHeightsInCell
	fsW := w0 / fullCharsInWeekCol
	if fsH <= fsW {
		// 高度更紧张：采用高度定出的字号，并反推（收窄）基准列宽
		ly.fontMM = fsH
		ly.cellWMM = fsH * fullCharsInWeekCol
	} else {
		// 宽度更紧张：列宽保持不变（恰为 4.5 全角字符），字号由宽度定
		ly.fontMM = fsW
		ly.cellWMM = w0
	}
	ly.fontMM *= 0.85

	// 各列宽（mm）：A/B/P 半格、周次列整格；Q = 右侧自动补全（≥ 2 格）
	half := ly.cellWMM / 2
	ly.colMM = make([]float64, 0, s.NumWeeks+4)
	ly.colMM = append(ly.colMM, half, half) // A 星期, B 节次
	for w := 1; w <= s.NumWeeks; w++ {
		ly.colMM = append(ly.colMM, ly.cellWMM)
	}
	ly.colMM = append(ly.colMM, half) // P 节次列
	q := ly.usableWMM - (half*2 + float64(s.NumWeeks)*ly.cellWMM + half)
	if q < cellWTimeCol*ly.cellWMM {
		q = cellWTimeCol * ly.cellWMM
	}
	ly.colMM = append(ly.colMM, q) // Q 时间/课程信息列

	ly.colPct = make([]float64, len(ly.colMM))
	for i, mm := range ly.colMM {
		ly.colPct[i] = mm / ly.usableWMM * 100
	}

	// 行高（供信息区文本等使用，与单元格等高；以百分比 = 相对字号）
	ly.lineHMM = ly.cellHMM
	// 标题字号略小于两格高度以便垂直居中，允许与正文不同
	ly.titleFontMM = ly.titleHMM * 0.85
	return ly
}

// courseSeqByID 返回 CourseID → 展示序号（从 1 起）。
// 序号与右侧"课程信息表"的条目编号同源（均按 CourseID 字典序），
// 保证课表格中的课程号与右侧信息表一一对应。
func (s *Schedule) courseSeqByID() map[string]int {
	m := map[string]int{}
	for i, ci := range s.CourseInfos() {
		m[ci.CourseID] = i + 1
	}
	return m
}

// cellInfo 返回某一（周、星期、节次）格子命中的元素及其显示文本；无课则空。
// 文本规则：
//
//	课程起始格 → "{课程展示序号} {FitName(课程名)}"
//	           （序号 = 该课程在右侧课程信息表中的编号，同一课程在课表中同号；
//	             实际节次由行位置确定，不再重复标注节次数字）
//	连堂后续节次格 → "*"
func (s *Schedule) cellInfo(week, weekday, slot int) (string, *CourseElement) {
	seq := s.courseSeqByID()
	for i := range s.Elements {
		e := &s.Elements[i]
		if e.Weekday != weekday || !e.HasWeek(week) || slot < e.StartSlot || slot > e.EndSlot {
			continue
		}
		if slot == e.StartSlot {
			no := seq[e.CourseID] // 课程展示序号，与右侧课程信息表一致
			return fmt.Sprintf("%d %s", no, FitName(e.Name, CellNameBudget)), e
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
// 页面固定为 A4 纸张大小；所有行高、列宽、字号均由 calcLayout 一次计算，
// 列宽与行高以百分比、字号以 mm 输出；表格下方不输出任何提示文字。
func (s *Schedule) RenderHTML(w io.Writer, opts RenderOptions) error {
	if err := s.Validate(); err != nil {
		return fmt.Errorf("渲染被拒绝：课表数据不合法: %w", err)
	}
	esc := func(v string) string { return template.HTMLEscapeString(v) }

	ly := s.calcLayout(opts.Portrait)

	var b strings.Builder
	b.WriteString("<!DOCTYPE html>\n<html lang=\"zh-CN\">\n<head>\n<meta charset=\"UTF-8\">\n")
	fmt.Fprintf(&b, "<title>%s</title>\n", esc(s.Title))
	b.WriteString("<style>\n")
	// .page：整页固定为 A4 纸张尺寸（默认横向，Portrait 翻转纵向），内边距 6mm。
	// box-sizing:border-box 使内容可用区 = 纸面 − 12mm，与版式计算一致。
	fmt.Fprintf(&b, "  .page { width: %.1fmm; height: %.1fmm; margin: 0mm auto; padding: %.1fmm; overflow: hidden; box-sizing: border-box; background: white; }\n",
		ly.pageWMM, ly.pageHMM, pagePadMM)
	fmt.Fprintf(&b, "  html, body { margin: 0; padding: 0; }\n")
	fmt.Fprintf(&b, "  body { background: #eeeeee; font-family: 'Microsoft YaHei', Arial, sans-serif; color: #000; font-size: %.3fmm; }\n", ly.fontMM)
	// 标题占 2 个单元格高度，字号独立于正文
	fmt.Fprintf(&b, "  h1 { height: %.3fmm; line-height: %.3fmm; font-size: %.3fmm; margin: 0; text-align: center; overflow: hidden; }\n",
		ly.titleHMM, ly.titleHMM, ly.titleFontMM)
	b.WriteString("  h2.info-title { font-size: 1em; margin: 0 0 0.2mm; text-align: center; font-weight: bold; }\n")
	b.WriteString("  table { border-collapse: collapse; }\n")
	// 表格宽度恒等于容器；fixed 布局 + 100% 宽度 → 整表完整显示、无横向溢出
	b.WriteString("  table#main { width: 100%; table-layout: fixed; border: " + cssStrong + "; }\n")
	// 单元格高度固定 = cellH；文字高度全表一致（标题除外）
	fmt.Fprintf(&b, "  table#main th, table#main td { border: %s; box-sizing: border-box; height: %.3fmm; padding: 0 0.3mm; text-align: center; vertical-align: middle; overflow: hidden; }\n",
		cssThin, ly.cellHMM)
	b.WriteString("  thead { display: table-header-group; }\n")
	b.WriteString("  thead th { background: #f0f0f0; font-weight: bold; white-space: nowrap; }\n")
	b.WriteString("  thead tr:nth-child(2) th { border-bottom: " + cssStrong + "; }\n")
	b.WriteString("  td.weekday { background: #f0f0f0; font-weight: bold; white-space: nowrap; }\n")
	b.WriteString("  td.time { white-space: nowrap; }\n")
	b.WriteString("  td.ps { border-left: " + cssStrong + "; white-space: nowrap; }\n")
	b.WriteString("  td.course { white-space: nowrap; }\n")
	b.WriteString("  td.course.star { color: #555; }\n")
	// 右侧课程信息大格：宽度受容器约束、超宽自动换行；内容向上对齐。
	// 注意用更高优先级选择器（table#main td.info-cell）覆盖通用的
	// "vertical-align: middle"，确保浏览器真的顶对齐（此规则需在通用规则之后）。
	b.WriteString("  table#main td.info-cell { border-left: " + cssStrong + "; height: auto; text-align: left; vertical-align: top; white-space: normal; word-break: break-word; overflow-wrap: break-word; }\n")
	b.WriteString("  div.info-wrap { padding: 0 0.5mm 0 0.2mm; text-align: left; }\n")
	fmt.Fprintf(&b, "  p.ci { margin: 0 0 %.3fmm; line-height: %.3fmm; text-align: left; white-space: normal; word-break: break-word; }\n",
		ly.cellHMM, ly.lineHMM)
	b.WriteString("  span.ci-no { font-weight: bold; }\n")
	b.WriteString("  span.ci-name { font-weight: bold; }\n")
	b.WriteString("  .ci-line { color: #333; }\n")
	b.WriteString("</style>\n")
	b.WriteString("</head>\n<body>\n")

	// ---------- A4 页面容器 ----------
	b.WriteString("<div class=\"page\">\n")
	fmt.Fprintf(&b, "<h1>%s</h1>\n", esc(s.Title))

	// ---------- 主课表 ----------
	b.WriteString("<table id=\"main\">\n<colgroup>")
	for _, p := range ly.colPct {
		fmt.Fprintf(&b, "<col style=\"width:%.3f%%\">", p)
	}
	b.WriteString("</colgroup>\n<thead>\n<tr>")
	// 表头第 1 行：星期(A, 跨 2 行) | 周次(B1) | 周次编号 1..N | 节次-时间表(P1:Q1)
	b.WriteString("<th rowspan=\"2\">星期</th><th>周次</th>")
	for w := 1; w <= s.NumWeeks; w++ {
		fmt.Fprintf(&b, "<th>%d</th>", w)
	}
	b.WriteString("<th colspan=\"2\">节次-时间表</th>")
	b.WriteString("</tr>\n<tr>")
	// 表头第 2 行：节次(B2) | 每周一日期（强制单行） | 节次(P2)/时间(Q2)
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
			case day == Monday: // 周一区段：右侧显示节次-时间表（源数据 jcfaList 覆盖时以其为准）
				fmt.Fprintf(&b, "<td class=\"ps\">%d</td><td class=\"ps time\">%s</td>", slot, s.SlotTimeText(slot))
			case day == Tuesday && slot == 1:
				// 周二~周日右侧 P/Q 合并大格：课程信息表（按 CourseID 去重排序）
				fmt.Fprintf(&b, "<td class=\"info-cell\" colspan=\"2\" rowspan=\"%d\">", 6*SlotsPerDay)
				b.WriteString("<div class=\"info-wrap\">")
				b.WriteString("<h2 class=\"info-title\">课程信息</h2>")
				infos := s.CourseInfos()
				if len(infos) == 0 {
					b.WriteString("<p class=\"ci\">（本学期无课程）</p>")
				} else {
					for i := range infos {
						ci := &infos[i]
						// 每条课程信息为一个 <p>（左对齐），同一行展示 课名|教师|教室，
						// 超出列宽自动换行
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
			default:
			}
			b.WriteString("</tr>\n")
		}
	}
	b.WriteString("</tbody>\n</table>\n")
	b.WriteString("</div>\n") // .page
	b.WriteString("</body>\n</html>\n")

	_, err := io.WriteString(w, b.String())
	return err
}
