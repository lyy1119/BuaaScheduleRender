// Command render 把示例课表渲染为一个与《空课程表示例.xlsx》布局一致的静态网页，
// 输出到指定的 HTML 文件。网页完全静态（纯 HTML+CSS，无脚本），可直接打印。
//
// 用法：
//
//	go run ./cmd/render -out schedule.html                 // 横向（宽高比 1.414:1）
//	go run ./cmd/render -out schedule.html -portrait       // 竖向打印（比例翻转 1:1.414）
package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	schedule "github.com/lyy1119/BuaaScheduleRender"
)

func main() {
	out := flag.String("out", "schedule.html", "输出的 HTML 文件路径")
	portrait := flag.Bool("portrait", false, "竖版打印（翻转宽高比为 1:1.414）")
	flag.Parse()

	s := schedule.NewSampleSchedule()
	if err := s.Validate(); err != nil {
		log.Fatalf("示例课表数据不合法: %v", err)
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
	orient := "横向 1.414:1"
	if *portrait {
		orient = "纵向 1:1.414"
	}
	fmt.Printf("已生成课表网页: %s（第 1 周周一 %s，共 %d 周，课程 %d 门，版面 %s）\n",
		*out, s.FirstMonday.Format("2006-01-02"), s.NumWeeks, len(s.CourseInfos()), orient)
}
