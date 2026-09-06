package schedule

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
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
	JCFAList json.RawMessage    `json:"jcfaList"`
}

type xskbRWRecord struct {
	XNXQMC string `json:"XNXQMC"` // 学年学期名，如 "2026-2027学年 第一学期"
	KCMC   string `json:"KCMC"`
	BJMC   string `json:"BJMC"`
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
	}
	if err := s.Validate(); err != nil {
		return nil, fmt.Errorf("解析出的课表不合法: %w", err)
	}
	return s, nil
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
