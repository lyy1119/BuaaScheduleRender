// Command render 把课表渲染为一个静态网页 HTML 文件输出到指定路径。
// 网页完全静态（纯 HTML+CSS，无脚本），可直接打印。
//
// 数据源（三选一，优先级 url > data > 内置示例）：
//
//	go run ./cmd/render -url  http://10.124.37.11:8778/ -out schedule.html   // 拉取教务接口
//	go run ./cmd/render -data testdata/loadXskbData.json -out schedule.html // 本地 JSON 文件
//	go run ./cmd/render -out schedule.html                                  // 内置示例课表
//
// 其它选项：
//
//	-first "2026-09-07"   学期第 1 周周一的日期（源数据不含时必填）
//	-portrait             竖版打印（A4 纵向）
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	schedule "github.com/lyy1119/BuaaScheduleRender"
)

func main() {
	out := flag.String("out", "schedule.html", "输出的 HTML 文件路径")
	portrait := flag.Bool("portrait", false, "竖版打印（A4 纵向）")
	dataFile := flag.String("data", "", "教务 JSON 文件路径（testdata/loadXskbData.json 等）")
	dataURL := flag.String("url", "", "教务数据地址（http://10.124.37.11:8778/）")
	first := flag.String("first", "2026-09-07", "学期第 1 周周一的日期 YYYY-MM-DD")
	flag.Parse()

	firstMonday, err := time.ParseInLocation("2006-01-02", *first, time.Local)
	if err != nil {
		log.Fatalf("-first 日期格式错误（应为 YYYY-MM-DD）: %v", err)
	}

	// ---- 准备 Schedule ----
	var s *schedule.Schedule
	switch {
	case *dataURL != "":
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		data, err := schedule.FetchXskb(ctx, *dataURL)
		if err != nil {
			log.Fatalf("拉取教务数据失败: %v", err)
		}
		s, err = schedule.ParseXskb(data, schedule.ParseOptions{FirstMonday: firstMonday})
		if err != nil {
			log.Fatalf("解析教务数据失败: %v", err)
		}
	case *dataFile != "":
		data, err := os.ReadFile(*dataFile)
		if err != nil {
			log.Fatalf("读取数据文件失败: %v", err)
		}
		s, err = schedule.ParseXskb(data, schedule.ParseOptions{FirstMonday: firstMonday})
		if err != nil {
			log.Fatalf("解析数据文件失败: %v", err)
		}
	default:
		s = schedule.NewSampleSchedule()
	}
	if err := s.Validate(); err != nil {
		log.Fatalf("课表数据不合法: %v", err)
	}

	f, err := os.Create(*out)
	if err != nil {
		log.Fatalf("无法创建输出文件: %v", err)
	}
	defer f.Close()

	opts := schedule.RenderOptions{Portrait: *portrait}
	if err := s.RenderHTML(f, opts); err != nil {
		log.Fatalf("渲染失败: %v", err)
	}
	orient := "横向"
	if *portrait {
		orient = "纵向"
	}
	fmt.Printf("已生成课表网页: %s（标题 %q，第 1 周周一 %s，共 %d 周，课程 %d 门，版面 A4 %s）\n",
		*out, s.Title, s.FirstMonday.Format("2006-01-02"), s.NumWeeks, len(s.CourseInfos()), orient)
}
