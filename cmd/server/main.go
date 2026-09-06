// Command server 启动课表 Web 服务：同一端口提供 登录 / 学期 / 课表 三个页面。
//
// 若通过 -user/-pass（或环境变量 XSKB_USER / XSKB_PASS）提供账号密码，
// 服务进入自动登录模式，跳过登录页。
//
//	go run ./cmd/server -addr :8080
//	go run ./cmd/server -addr :8080 -user 学号 -pass 密码        # 自动登录模式
//	XSKB_USER=学号 XSKB_PASS=密码 go run ./cmd/server -addr :8080
//
// 其它选项：-first "2026-09-07"（学期第 1 周周一），-landscape（A4 横向，默认竖向）。
package main

import (
	"flag"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/lyy1119/BuaaScheduleRender/web"
)

func main() {
	addr := flag.String("addr", ":8080", "监听地址")
	user := flag.String("user", os.Getenv("XSKB_USER"), "北航统一身份认证账号（自动登录模式；也可用环境变量 XSKB_USER）")
	pass := flag.String("pass", os.Getenv("XSKB_PASS"), "账号密码（含特殊字符时推荐优先用环境变量 XSKB_PASS 传递，避免 shell 转义问题）")
	first := flag.String("first", "", "学期第 1 周周一的日期 YYYY-MM-DD（留空则按课表数据自动推算）")
	landscape := flag.Bool("landscape", false, "输出 A4 横向（默认竖向）")
	flag.Parse()

	var firstMonday time.Time
	if *first != "" {
		var err error
		firstMonday, err = time.ParseInLocation("2006-01-02", *first, time.Local)
		if err != nil {
			log.Fatalf("-first 日期格式错误: %v", err)
		}
	}
	cfg := web.Config{
		Username:    *user,
		Password:    *pass,
		FirstMonday: firstMonday,
		Portrait:    !*landscape,
		Logger:      log.New(os.Stdout, "[web] ", log.LstdFlags),
	}
	if cfg.Username != "" {
		log.Printf("自动登录模式已启用（账号 %s，跳过登录页）", cfg.Username)
	} else {
		log.Printf("手动登录模式（访问 / 开始）")
	}
	srv := web.New(cfg)
	log.Printf("课表服务监听 %s（-first 未提供时将按课表数据自动推算第 1 周周一）", *addr)
	if err := http.ListenAndServe(*addr, srv.Handler()); err != nil {
		log.Fatalf("服务启动失败: %v", err)
	}
}
