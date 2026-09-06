package schedule

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// 本文件实现"教务源数据 → Schedule"的解析。
//
// 真实数据来自 http://10.124.37.11:8778/ 返回的 loadXskbData JSON，结构：
//
//	{
//	  "code": 1,
//	  "jgList": [ { …课程安排记录… }, … ],   // 已整理好的"课程信息"，一条记录即一段上课安排
//	  "rwList": [ … ],                        // 任课课程清单（含学年学期名称等）
//	  "jcfaList": [ … ]                       // 节次方案（各节起止时间）
//	}
//
// jgList 中的每条记录（我们直接映射为一个"课程元素"）已包含全部所需信息：
//
//	KCDM    课程代码  → CourseID（同课程多段/多位老师共享同一代码）
//	KCMC    课程名称  → Name
//	JGJSXM  任课教师  → Teacher（同一课程可由多位老师分别授课）
//	JASMC   教室      → Location
//	XQ      星期几    → Weekday（1=周一 … 7=周日）
//	KSJCDM / JSJCDM  起止节次 → StartSlot / EndSlot
//	ZCBH    周次位串（30 位二进制，"1"=该周上课）→ WeekMask
//
// 注意：该数据已经按"课程"整理（教师/教室/周次都在记录内），因此不需要再
// 用每日课表去合成课程信息。课程总周数 NumWeeks 与学期第 1 周周一的日期
// FirstMonday 不在该 JSON 中，由调用方通过 ParseOptions 提供（或自动推断周数）。

// XskbCourseRecord 对应 jgList 中的一条课程安排记录。
type XskbCourseRecord struct {
	KCDM    string `json:"KCDM"`    // 课程代码（CourseID）
	KCMC    string `json:"KCMC"`    // 课程名
	JGJSXM  string `json:"JGJSXM"`  // 任课教师
	JASMC   string `json:"JASMC"`   // 教室
	JASXQMC string `json:"JASXQMC"` // 校区（如 沙河校区）
	XQ      int    `json:"XQ"`      // 星期几 1..7
	KSJCDM  int    `json:"KSJCDM"`  // 起始节次
	JSJCDM  int    `json:"JSJCDM"`  // 结束节次
	ZCBH    string `json:"ZCBH"`    // 周次位串："1" 表示该周上课
	BJMC    string `json:"BJMC"`    // 班级名（可选）
}

type xskbEnvelope struct {
	Code     int                `json:"code"`
	JGList   []XskbCourseRecord `json:"jgList"`
	RWList   []xskbRWRecord     `json:"rwList"`
	JCFAList []xskbJCFA         `json:"jcfaList"`
}

// xskbJCFA 对应 jcfaList 中的一个节次方案；skjcList 为该方案下每一节的
// 序号与起止时间（KSSJ/JSSJ 为 HHMM 整数，如 800 = 08:00）。
type xskbJCFA struct {
	SkjcList []xskbJC `json:"skjcList"`
}

type xskbJC struct {
	DM   string `json:"DM"`   // 节次序号："1".."14"
	KSSJ int    `json:"KSSJ"` // 开始时间，HHMM 编码，如 800
	JSSJ int    `json:"JSSJ"` // 结束时间，HHMM 编码，如 845
}

type xskbRWRecord struct {
	XNXQMC string `json:"XNXQMC"` // 学年学期名，如 "2026-2027学年 第一学期"
	KCDM   string `json:"KCDM"`
	KCMC   string `json:"KCMC"`
	BJMC   string `json:"BJMC"`
	RKJS   string `json:"RKJS"`   // 任课教师（逗号分隔，已按课程聚合）
	PKSJDD string `json:"PKSJDD"` // 排课地点时间描述（"周次 星期[节次]教室;…"）
	XQMC   string `json:"XQMC"`   // 校区名
	KKDWMC string `json:"KKDWMC"` // 开课单位
}

// courseInfosFromRW 把 rwList（教务接口中已按课程代码聚合好的课程清单）
// 转换为课程信息表数据：教师取自 RKJS（逗号分隔，本身就是多位教师的合并结果），
// 教室从 PKSJDD 每段"…节]<教室>"文本提取并去重。
// 说明：数据源已聚合好课程信息，因此不再用 Elements 做第二遍聚合。
func courseInfosFromRW(rw []xskbRWRecord) []CourseInfo {
	out := make([]CourseInfo, 0, len(rw))
	for _, r := range rw {
		if r.KCDM == "" {
			continue
		}
		ci := CourseInfo{CourseID: r.KCDM, Name: r.KCMC}
		// RKJS："张振华,程林,李家军,张余"（兼容中文逗号）
		for _, t := range strings.Split(strings.ReplaceAll(r.RKJS, "，", ","), ",") {
			t = strings.TrimSpace(t)
			if t != "" && !containsStr(ci.Teachers, t) {
				ci.Teachers = append(ci.Teachers, t)
			}
		}
		// PKSJDD：段以 ";" 分隔（兼容中文分号），教室位于该段 "…节]教室"
		for _, seg := range strings.Split(strings.ReplaceAll(r.PKSJDD, "；", ";"), ";") {
			if i := strings.LastIndex(seg, "]"); i >= 0 {
				loc := strings.TrimSpace(seg[i+1:])
				if loc != "" && !containsStr(ci.Locations, loc) {
					ci.Locations = append(ci.Locations, loc)
				}
			}
		}
		out = append(out, ci)
	}
	return out
}

func containsStr(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// ParseOptions 控制源数据解析。
type ParseOptions struct {
	// FirstMonday 学期第 1 周周一的日期（真实数据里不包含，必须提供）。
	FirstMonday time.Time
	// NumWeeks 学期总周数；<=0 时自动取所有记录中的最大开课周。
	NumWeeks int
	// MergeAdjacent 是否把同一安排（同课程/周次/星期/教室/教师）拆开的相邻单节
	// 合并为连堂元素（如 11、12 节两条 → 1 个 11-12 元素，渲染时显示 *）。
	// 默认 true。
	MergeAdjacent *bool
}

// ParseXskb 把教务接口返回的 JSON 字节解析为 *Schedule。
func ParseXskb(data []byte, opts ParseOptions) (*Schedule, error) {
	var env xskbEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("解析教务 JSON 失败: %w", err)
	}
	if env.Code != 1 {
		return nil, fmt.Errorf("教务接口返回 code=%d（非 1）", env.Code)
	}

	merge := true
	if opts.MergeAdjacent != nil {
		merge = *opts.MergeAdjacent
	}

	// 由记录直接构造课程元素（记录即整理好的课程信息）
	elements, maxWeek, err := buildElements(env.JGList, merge)
	if err != nil {
		return nil, err
	}

	numWeeks := opts.NumWeeks
	if numWeeks <= 0 {
		numWeeks = maxWeek
	}
	if numWeeks < 1 {
		numWeeks = 13 // 完全没有开课记录时的兜底
	}

	title := "课表"
	if len(env.RWList) > 0 && env.RWList[0].XNXQMC != "" {
		title = env.RWList[0].XNXQMC + "课表"
	}
	s := &Schedule{
		Title:       title,
		FirstMonday: opts.FirstMonday,
		NumWeeks:    numWeeks,
		Elements:    elements,
		// 课程信息表直接使用源数据已聚合好的 rwList（不再二次聚合）
		CourseList: courseInfosFromRW(env.RWList),
	}
	// 节次时间表：数据若提供 jcfaList.skjcList，则解析并覆盖默认时间表
	if times, ok := slotTimesFromJCFA(env.JCFAList); ok {
		s.SlotTimes = times
	}
	if err := s.Validate(); err != nil {
		return nil, fmt.Errorf("解析出的课表不合法: %w", err)
	}
	return s, nil
}

// slotTimesFromJCFA 解析 jcfaList 节次方案，返回与节次 1..14 对齐的时间表
// （第 i 项对应第 i+1 节，形如 "08:00-08:45"；缺失的节次为空串）。
// 未提供时间表时返回 ok=false，调用方沿用默认时间表。
func slotTimesFromJCFA(jcfa []xskbJCFA) ([]string, bool) {
	table := make([]string, SlotsPerDay)
	found := false
	for _, fa := range jcfa {
		for _, jc := range fa.SkjcList {
			dm := 0
			if _, err := fmt.Sscanf(jc.DM, "%d", &dm); err != nil || dm < 1 || dm > SlotsPerDay {
				continue
			}
			text, err := formatHMMRange(jc.KSSJ, jc.JSSJ)
			if err != nil {
				continue
			}
			table[dm-1] = text
			found = true
		}
	}
	return table, found
}

// formatHMMRange 把 HHMM 编码的起止时刻格式化为 "hh:mm-hh:mm"。
func formatHMMRange(ks, js int) (string, error) {
	kh, km := ks/100, ks%100
	jh, jm := js/100, js%100
	if kh < 0 || kh > 23 || km < 0 || km > 59 || jh < 0 || jh > 23 || jm < 0 || jm > 59 {
		return "", fmt.Errorf("非法时间 %d-%d", ks, js)
	}
	return fmt.Sprintf("%02d:%02d-%02d:%02d", kh, km, jh, jm), nil
}

// buildElements 把课程安排记录映射为 CourseElement。
// 若 merge 为 true，同一安排（同 CourseID/周次/星期/教室/教师）相邻节次记录
// 会被合并为一条连堂元素。
func buildElements(records []XskbCourseRecord, merge bool) ([]CourseElement, int, error) {
	maxWeek := 0
	if merge {
		// 先按键聚合，再对每个键内的节次排序并合并连续段
		type kv struct {
			id, name, teacher, loc string
			weekday                int
			mask                   uint32
		}
		groups := map[kv][]int{}
		for i, r := range records {
			mask, err := parseZCBH(r.ZCBH)
			if err != nil {
				return nil, 0, fmt.Errorf("记录 #%d (%s %s): %w", i+1, r.KCDM, r.KCMC, err)
			}
			if w := maxSetWeek(mask); w > maxWeek {
				maxWeek = w
			}
			k := kv{r.KCDM, r.KCMC, r.JGJSXM, r.JASMC, r.XQ, mask}
			for sl := r.KSJCDM; sl <= r.JSJCDM; sl++ {
				groups[k] = append(groups[k], sl)
			}
		}
		var out []CourseElement
		for k, slots := range groups {
			sort.Ints(slots)
			segStart := slots[0]
			prev := slots[0]
			flush := func(end int) {
				out = append(out, CourseElement{
					CourseID: k.id, Name: k.name, Teacher: k.teacher, Location: k.loc,
					Weekday: k.weekday, StartSlot: segStart, EndSlot: end,
					WeekMask: uint32(k.mask),
				})
			}
			for _, sl := range slots[1:] {
				if sl == prev+1 {
					prev = sl
					continue
				}
				flush(prev)
				segStart, prev = sl, sl
			}
			flush(prev)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].CourseID < out[j].CourseID })
		return out, maxWeek, nil
	}

	// 不合并：每条记录原样生成一个元素
	out := make([]CourseElement, 0, len(records))
	for i, r := range records {
		mask, err := parseZCBH(r.ZCBH)
		if err != nil {
			return nil, 0, fmt.Errorf("记录 #%d (%s %s): %w", i+1, r.KCDM, r.KCMC, err)
		}
		if w := maxSetWeek(mask); w > maxWeek {
			maxWeek = w
		}
		out = append(out, CourseElement{
			CourseID: r.KCDM, Name: r.KCMC, Teacher: r.JGJSXM, Location: r.JASMC,
			Weekday: r.XQ, StartSlot: r.KSJCDM, EndSlot: r.JSJCDM, WeekMask: mask,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CourseID < out[j].CourseID })
	return out, maxWeek, nil
}

// parseZCBH 把形如 "111101111000000000000000000000" 的周次位串解析为位掩码：
// 位串第 i 位（从 0 开始）为 '1' ⇔ 第 i+1 周上课 ⇔ WeekMask 第 i 位为 1。
func parseZCBH(s string) (uint32, error) {
	var m uint32
	if len(s) > 32 {
		return 0, fmt.Errorf("周次位串长度 %d 超过 32 位，无法用 uint32 掩码表示", len(s))
	}
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '1':
			m |= 1 << i
		case '0':
		default:
			return 0, fmt.Errorf("周次位串含非法字符 %q", s[i])
		}
	}
	if m == 0 {
		return 0, errors.New("周次位串全为 0（该记录没有任何一周上课）")
	}
	return m, nil
}

// maxSetWeek 返回掩码中置位的最大周（1 起），无置位返回 0。
func maxSetWeek(mask uint32) int {
	w := 0
	for m := mask; m != 0; m >>= 1 {
		w++
	}
	return w
}

// FetchXskb 请求教务数据地址并返回原始 JSON 字节。
func FetchXskb(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求教务数据 %s 失败: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("请求教务数据 %s 返回状态 %s", url, resp.Status)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return data, nil
}
