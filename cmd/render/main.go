// Command render 把示例课表渲染成一个与《样本.xlsx》布局一致的网页表格，
// 输出到指定的 HTML 文件。
//
// 用法：
//
//	go run ./cmd/render -out schedule.html
//	go run ./cmd/render -out schedule.html -location=false -teacher=true
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
	showLocation := flag.Bool("location", true, "课程格内显示上课地点")
	showTeacher := flag.Bool("teacher", false, "课程格内显示任课教师")
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

	opts := schedule.RenderOptions{ShowLocation: *showLocation, ShowTeacher: *showTeacher}
	if err := s.RenderHTML(f, opts); err != nil {
		log.Fatalf("渲染失败: %v", err)
	}
	fmt.Printf("已生成课表网页: %s（第 1 周周一 %s，共 %d 周）\n",
		*out, s.FirstMonday.Format("2006-01-02"), s.NumWeeks)
}
