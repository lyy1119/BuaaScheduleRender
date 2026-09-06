// Command fetch 通过北航统一身份认证自动登录后，抓取研究生教务（GSMIS）
// 某一学期的课表原始 JSON 并保存到文件或打印到标准输出。
//
// 用法：
//
//	go run ./cmd/fetch -user 学号 -pass 密码 -out xskb.json          // 学期自动推断
//	go run ./cmd/fetch -user 学号 -pass 密码 -sem 20261 -out xskb.json // 手动指定学期
//
// 账号密码也可通过环境变量 XSKB_USER / XSKB_PASS 传入，避免出现在命令行。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/lyy1119/BuaaScheduleRender/fetch"
)

func main() {
	user := flag.String("user", os.Getenv("XSKB_USER"), "北航统一身份认证账号（或用环境变量 XSKB_USER）")
	pass := flag.String("pass", os.Getenv("XSKB_PASS"), "账号密码（含特殊字符时推荐优先用环境变量 XSKB_PASS 传递，避免 shell 转义问题）")
	sem := flag.String("sem", "", "5 位学期号，如 20261；为空按当前时间自动推断")
	out := flag.String("out", "", "输出 JSON 文件路径；为空则打印到标准输出")
	flag.Parse()

	if *user == "" || *pass == "" {
		log.Fatal("缺少账号/密码：请用 -user/-pass 或环境变量 XSKB_USER/XSKB_PASS 提供")
	}
	if *sem == "" {
		*sem = fetch.AutoSemester(time.Now())
		fmt.Fprintf(os.Stderr, "自动推断学期: %s\n", *sem)
	}

	c := fetch.NewClient(*user, *pass)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	data, err := c.FetchJSON(ctx, *sem)
	if err != nil {
		log.Fatalf("获取课表数据失败: %v", err)
	}

	if *out == "" {
		os.Stdout.Write(data)
		fmt.Println()
		return
	}
	if err := os.WriteFile(*out, data, 0o644); err != nil {
		log.Fatalf("写入文件失败: %v", err)
	}
	fmt.Printf("已保存 %s（%d 字节，学期 %s）\n", *out, len(data), *sem)
}
