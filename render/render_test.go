package render

import (
	"strings"
	"testing"

	schedule "github.com/lyy1119/BuaaScheduleRender"
)

func TestFitName(t *testing.T) {
	cases := []struct {
		name string
		want string
	}{
		{"高等数学A(上)", "高等数学"},
		{"线性代数", "线性代数"}, // 短于预算，原样保留
		{"大学英语(二)", "大学英语"},
		{"Python程序设计(选修)", "Pyth"},
	}
	for _, c := range cases {
		if got := FitName(c.name, CellNameBudget); got != c.want {
			t.Errorf("FitName(%q, %d) = %q, want %q", c.name, CellNameBudget, got, c.want)
		}
	}
	if got := FitName("高等数学", 0); got != "" {
		t.Errorf("FitName 预算 0 应返回空串, got %q", got)
	}
}

// TestWeekMaskBits 验证掩码语义与 1-3 周有课、第 4 周停课、第 5 周恢复。

func TestCellTextRules(t *testing.T) {
	s := schedule.NewSampleSchedule()
	cases := []struct {
		week, weekday, slot int
		want                string
	}{
		{1, schedule.Monday, 1, "3 高等数学"},    // 高数=序号3（M-101），课名截 4 字
		{1, schedule.Monday, 2, "#"},         // 连堂续节
		{1, schedule.Wednesday, 3, "3 高等数学"}, // 同一课程另一元素，序号相同
		{1, schedule.Wednesday, 4, "#"},
		{1, schedule.Friday, 3, "4 大学物理"},     // 大物=序号4（P-101）
		{2, schedule.Friday, 3, ""},           // 双周停课（单周课）
		{4, schedule.Friday, 3, ""},           // 双周停课
		{7, schedule.Wednesday, 11, "5 形势与政"}, // 形策=序号5（P-301）；5 字课名截 4 字
		{6, schedule.Wednesday, 11, ""},
		{3, schedule.Saturday, 1, "1 Pyth"}, // Python=序号1（CS-501），第 3 周起
		{2, schedule.Tuesday, 6, "7 线性代数"},  // 线代=序号7（lin-alg-02）
		{2, schedule.Tuesday, 7, "#"},
		{1, schedule.Saturday, 1, ""}, // Python 第 3 周才开始
	}
	for _, c := range cases {
		if got := CellText(s, c.week, c.weekday, c.slot); got != c.want {
			t.Errorf("CellText(%d,%d,%d) = %q, want %q", c.week, c.weekday, c.slot, got, c.want)
		}
	}
}

// TestCourseInfoSortAndDedup 验证课程信息表：
// 按 CourseID 字典序排序（真实 ID 为非连续字符串）、去重（高数/英语仅一条）。

func TestRenderHTML(t *testing.T) {
	s := schedule.NewSampleSchedule()
	var b strings.Builder
	if err := RenderHTML(&b, s, RenderOptions{}); err != nil {
		t.Fatal(err)
	}
	html := b.String()
	for _, want := range []string{
		"<!DOCTYPE html>", "2026-2027学年第一学期课表（示例）",
		"9月7日", "9月14日", "11月30日", "节次-时间表", "08:00-08:45",
		"3 高等数学", "#", "4 大学物理", "5 形势与政", // 课程格 = 课程序号+截断课名
		"table#main td.info-cell", // 信息区顶对齐规则
		`<div class="page">`,      // A4 页面容器
		".page { width: 297.0mm",  // 默认横向纸张
		"table-layout: fixed",     // 表格宽度恒等于容器（整表完整显示）
		"width: 100%",             // 不产生横向溢出
		"font-size:",              // 字号以 mm 输出
	} {
		if !strings.Contains(html, want) {
			t.Errorf("渲染结果缺少 %q", want)
		}
	}
	// 表格下方不再输出起始日期/打印提示等文字
	for _, forbid := range []string{"第 1 周周一起始日期", "打印提示", "class=\"foot\"", "class=\"tip\"", "viewport", "overflow: auto", "overflow: scroll", "sheet landscape", "@page"} {
		if strings.Contains(html, forbid) {
			t.Errorf("渲染结果不应包含 %q", forbid)
		}
	}
	// 列数量 = NumWeeks + 4（A/B + 13 周次 + P/Q）
	if got := strings.Count(html, "<col "); got != 13+4 {
		t.Errorf("colgroup 列数 = %d, want 17", got)
	}
	// 课程信息表：7 条 <p>、按 CourseID 排序、带 1..7 展示序号
	if got := strings.Count(html, `<p class="ci">`); got != 7 {
		t.Errorf("课程信息表条目数 = %d, want 7", got)
	}
	for _, want := range []string{
		`<span class="ci-no">1.</span> <span class="ci-name">Python程序设计(选修)</span>`,
		`<span class="ci-no">3.</span> <span class="ci-name">高等数学A(上)</span>`,
		`<span class="ci-no">7.</span> <span class="ci-name">线性代数</span>`,
		`| 王建国、李敏`,        // 同一课程多教师同行合并展示
		`| 教3-105、主M-201`, // 同一课程多教室同行合并展示
	} {
		if !strings.Contains(html, want) {
			t.Errorf("课程信息表缺少排序条目 %q", want)
		}
	}
	// 连堂标记说明段落存在
	if !strings.Contains(html, "# 符号代表") || !strings.Contains(html, "p.ci-note") {
		t.Error("缺少 # 连堂标记的说明段落 (p.ci-note)")
	}
	// 课程信息不使用 <ol>/<li> 列表元素，改用 <p>
	for _, forbid := range []string{"<ol", "<li>", "</li>", "ci-times", "（高数A）", "上课时间"} {
		if strings.Contains(html, forbid) {
			t.Errorf("课程信息表不应包含 %q", forbid)
		}
	}
	// 日期锚定：最长日期形态
	if DateMaxText != "12月31日" {
		t.Errorf("DateMaxText = %q, want 12月31日", DateMaxText)
	}
	// 课程格文本统计："3 高等数学" = 高数两个元素(周一 1-2 与周三 3-4)的起始格
	// 每列 1 格 × 13 周 × 2 段 = 26
	if got := strings.Count(html, "3 高等数学"); got != 26 {
		t.Errorf(`"3 高等数学" 出现 %d 次, want 26`, got)
	}
}

// TestCalcLayout 验证版式计算的联立约束：
// 行高 × (100 表格行 + 2 标题行) ≈ 可用高度；周次列 ≥ 4.5×字号；
// 单元格 ≥ 1.5×字号；各列百分比合计 ≈ 100%；列数 = 周数 + 4。

func TestCalcLayout(t *testing.T) {
	s := schedule.NewSampleSchedule()
	if s.NumWeeks != 13 {
		t.Fatalf("示例周数 = %d", s.NumWeeks)
	}
	ly := calcLayout(s, false)
	if got := len(ly.colPct); got != s.NumWeeks+4 {
		t.Fatalf("colPct 长度 = %d, want %d", got, s.NumWeeks+4)
	}
	// 高度约束：(100+2)×cellH = 可用高（210-12）
	wantH := 210.0 - 12.0
	if got := ly.cellHMM * float64(tableCellRows+titleCellRows); got > wantH+1e-6 {
		t.Errorf("总行高 %.4f 超过可用高度 %.4f", got, wantH)
	}
	// 字体须同时满足高度(≤cellH/1.5)与宽度(周列 4.5 全角)约束
	if ly.fontMM > ly.cellHMM/charHeightsInCell+1e-9 {
		t.Errorf("font %.4f 超出 高度/1.5 = %.4f", ly.fontMM, ly.cellHMM/charHeightsInCell)
	}
	if ly.cellWMM < ly.fontMM*fullCharsInWeekCol-1e-9 {
		t.Errorf("周列宽 %.4f 不足以容纳 4.5 字符(需 %.4f)", ly.cellWMM, ly.fontMM*fullCharsInWeekCol)
	}
	// 列宽百分比合计 ≈ 100%
	sum := 0.0
	for _, p := range ly.colPct {
		sum += p
	}
	if sum < 99.5 || sum > 100.5 {
		t.Errorf("colPct 合计 = %.2f%%，应约为 100%%", sum)
	}
	// 标题占 titleCellRows 格高（按用户 CSS 调整为 1.5）
	if got := ly.titleHMM; got != titleCellRows*ly.cellHMM {
		t.Errorf("titleHMM = %.4f, want titleCellRows×cellH = %.4f", got, titleCellRows*ly.cellHMM)
	}
	// 竖版翻转纸张
	lyp := calcLayout(s, true)
	if lyp.pageWMM != 210 || lyp.pageHMM != 297 {
		t.Errorf("竖版纸张 = %.0f×%.0f, want 210×297", lyp.pageWMM, lyp.pageHMM)
	}
}

// TestRenderHTMLPortrait 验证竖版渲染：A4 纵向纸张。

func TestRenderHTMLPortrait(t *testing.T) {
	s := schedule.NewSampleSchedule()
	var b strings.Builder
	if err := RenderHTML(&b, s, RenderOptions{Portrait: true}); err != nil {
		t.Fatal(err)
	}
	html := b.String()
	for _, want := range []string{
		`<div class="page">`,
		".page { width: 210.0mm; height: 297.0mm", // 翻转后的 A4 纵向纸张
	} {
		if !strings.Contains(html, want) {
			t.Errorf("竖版渲染缺少 %q", want)
		}
	}
	if strings.Contains(html, ".page { width: 297.0mm; height: 210.0mm") {
		t.Error("竖版渲染不应使用横向纸张尺寸")
	}
}

// TestDefaultRenderOptions 验证默认渲染方向为 A4 竖向。

func TestDefaultRenderOptions(t *testing.T) {
	if !DefaultRenderOptions().Portrait {
		t.Error("默认渲染方向应为 A4 竖向（Portrait=true）")
	}
}
