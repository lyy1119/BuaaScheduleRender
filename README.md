# BuaaScheduleRender

<div align="center">

<img src="https://raw.githubusercontent.com/lyy1119/Imgs/main/img/BSR.png" alt="logo" width="254">

</div>

北航研究生课表**自动获取与渲染**工具：统一身份认证登录 → 抓取教务系统课表数据 →
解析为课表模型 → 渲染成 A4 版式的静态 HTML → 通过 Web 页面查看与打印。

> **打印说明** ： A开张的纸张长宽比固定，从小到大一个型号的纸张，可以放大 $\sqrt{2}$ 实现。使用浏览器打印pdf或者直接打印时，使用无边距，选择相应纸张，若非A4，按照 $\sqrt{2}$ 的比例缩放即可。

整体流程：
**自动登录（SSO）→ 抓取课表 JSON → 解析 → HTML 渲染 → Web 服务（多用户）**

## 功能特性

- **自动登录**：使用账号密码自动完成统一身份认证（CAS/SSO）登录，会话 Cookie
  自动维护，无需手动携带；也支持复用浏览器已有会话。
- **验证码支持**：当统一身份认证触发风控要求验证码时，登录页会直接显示验证码
  图片（点击图片或"换一张"可刷新），输入后继续登录；自动登录模式下该错误会
  明确提示为 `登录失败：… 需要验证码`。
- **课表抓取**：按学期（5 位学期号，如 `20261`）抓取教务课表数据；
  学期可按当前日期自动推断（9–1 月 → 第 1 学期，2–8 月 → 第 2 学期），也可手动指定。
- **开学日期自动推算**：由课表数据中课程的"首次上课日期"推算学期第 1 周周一，
  无需写死日期（无课程数据时回退为当年 1 月 1 日）。
- **A4 版面渲染**：静态 HTML（零脚本），列宽/行高/字号按 A4 纸张（横向或竖向）
  自动计算；课程名按列宽预算截断，连堂以 `#` 标记并有图例说明。
- **Web 服务**：登录 / 课表（含工具栏） / 纯课表打印页，同一端口、页面间跳转；
  课表页与纯课表页带强防缓存响应头；多用户会话隔离、支持并发。
- **HTTP API**：`/help` 提供机读契约（JSON）与人类可读的 Markdown 两种形态；
  `GET /api/v1/courses?date=YYYY-MM-DD` 按日期查询当天课程列表。
- **多入口**：`server`（Web）、`render`（命令行渲染 HTML 文件）、`fetch`（抓取 JSON）。

## 项目结构

```
├── schedule/ 模型与解析（根包）
│   ├── schedule.go    课表数据模型（Schedule/CourseElement/CourseInfo）与校验
│   ├── import.go      教务源数据 JSON 解析 + 开学日期推算
│   └── sample.go      内置示例课表
├── login/     统一身份认证（SSO/CAS）登录与会话 Cookie
├── fetch/     GSMIS 课表 JSON 抓取 + 学期推断
├── render/    A4 版式计算与 HTML 渲染
├── web/       课表 Web 服务（登录/课表/打印页 + 多用户会话 + 防缓存）
└── cmd/
    ├── server   Web 服务入口
    ├── render   命令行渲染 HTML 文件
    └── fetch    命令行抓取课表 JSON
```

## 快速开始

需要 Go 1.21+（Docker 方式见下文）。

```bash
# 1) 手动登录模式：访问 http://localhost:8080 登录
go run ./cmd/server -addr :8080

# 2) 自动登录模式：通过环境变量提供账号密码（推荐，避免特殊字符被 shell 转义）
XSKB_USER=你的账号 XSKB_PASS=你的密码 go run ./cmd/server -addr :8080

# 3) 等价命令行参数
go run ./cmd/server -addr :8080 -user 你的账号 -pass 你的密码
```

启动后访问：

| 路径 | 说明 |
| --- | --- |
| `/` | 入口：未登录跳 `/login`；已登录跳课表页 |
| `/login` | 登录页（手动模式；自动登录模式跳过） |
| `/schedule?sem=20261` | 课表页：工具栏（学期查询 / 强制刷新 / 打印 / 退出登录）+ 课表 |
| `/schedule/print?sem=20261` | 纯课表页（A4 HTML，可直接打印） |
| `/logout` | 退出登录 |
| `/help` | API 契约：默认 JSON；`?format=md` 或 `Accept: text/markdown` 返回 Markdown |
| `/api/v1/courses?date=2026-09-07` | 查询指定日期的课程列表（JSON） |

说明：
- 学期缺省按当前时间自动推断并预填；也可手动改 `sem` 查询；
- 课表与打印页均返回 `Cache-Control: no-store` 等防缓存头；
- 会话内课表有 3 分钟缓存（`fetchCacheTTL`），超过自动重抓；点击工具栏
  **强制刷新**（`?refresh=1`）可立即绕过缓存抓取最新课表。

## 配置项（cmd/server）

| 参数 / 环境变量 | 说明 | 默认 |
| --- | --- | --- |
| `-addr` | 监听地址 | `:8080` |
| `-user` / `XSKB_USER` | 统一身份认证账号（自动登录模式） | 空 |
| `-pass` / `XSKB_PASS` | 账号密码（自动登录模式） | 空 |
| `-first YYYY-MM-DD` | 学期第 1 周周一（留空自动推算） | 自动推算 |
| `-landscape` | 输出 A4 横向（默认竖向） | `false` |
| `-debug` | 调试日志（访问日志 / fetch 详情等） | `false` |

> 提示：密码含 `! ^ % #` 等特殊字符时，**优先用环境变量** `XSKB_PASS` 传递，
> 避免交互 shell 的历史展开/转义问题。

## HTTP API

完整契约见 `/help`（机读 JSON）或 `/help?format=md`（Markdown）。摘要：

| 接口 | 说明 |
| --- | --- |
| `GET /help` | 服务契约：约定、页面、接口、参数、字段、错误码与示例 |
| `GET /api/v1/courses` | 查询指定日期当天的课程列表 |

`GET /api/v1/courses` 参数：

- `date=YYYY-MM-DD`：查询日期（默认今天）
- `sem=20261`：学期号（省略时按 `date` 所在学期推断）
- `refresh=1`：绕过会话缓存，强制重新抓取

响应示例（节选）：

```json
{
  "service": "buaa-schedule",
  "api_version": "1",
  "semester": "20261",
  "date": "2026-09-11",
  "week": 1,
  "in_term": true,
  "weekday": 5,
  "weekday_name": "周五",
  "term_start": "2026-09-07",
  "num_weeks": 19,
  "course_count": 1,
  "courses": [
    {
      "course_id": "P-101",
      "name": "大学物理(上)",
      "teachers": ["陈平"],
      "locations": ["教5-101"],
      "weekday": 5,
      "start_slot": 3,
      "end_slot": 4,
      "start_time": "09:50",
      "end_time": "11:25"
    }
  ]
}
```

错误响应统一为 `{"error":{"code":"...","message":"..."}}`；未登录返回 `401`
（手动登录模式需先建立会话），非法日期返回 `400`，教务数据获取失败返回 `502`。

```bash
curl -s 'http://127.0.0.1:8080/api/v1/courses?date=2026-09-11' | jq .
curl -s 'http://127.0.0.1:8080/help?format=md'
```

## 命令行工具

```bash
# 抓取某学期课表原始 JSON（自动登录）
go run ./cmd/fetch -sem 20261 -out xskb.json

# 用本地 JSON 渲染 HTML 文件（默认 A4 竖向，-landscape 横向）
go run ./cmd/render -data xskb.json -out schedule.html

# 直接渲染内置示例课表
go run ./cmd/render -out schedule.html
```

## 跨平台编译（含 Windows）

本仓库未包含预编译产物，可按需在本机构建：

```bash
# Linux / macOS
go build -o dist/buaa-schedule-server ./cmd/server
go build -o dist/buaa-schedule-fetch ./cmd/fetch
go build -o dist/buaa-schedule-render ./cmd/render

# Windows（amd64），在任意平台均可交叉编译
GOOS=windows GOARCH=amd64 go build -o dist/buaa-schedule-server.exe ./cmd/server
GOOS=windows GOARCH=amd64 go build -o dist/buaa-schedule-fetch.exe ./cmd/fetch
GOOS=windows GOARCH=amd64 go build -o dist/buaa-schedule-render.exe ./cmd/render

# 在 Windows 上运行（PowerShell）
$env:XSKB_USER="你的账号"; $env:XSKB_PASS="你的密码"
.\buaa-schedule-server.exe -addr :8080
```

## Docker 部署

镜像为两阶段构建：`golang` 镜像编译（`CGO_ENABLED=0`，完全静态），
`scratch` 镜像运行（无 shell/包管理器/libc），并已内置上海时区与根证书。

```bash
# 使用模板（复制出来的 docker-compose.yml 已被 gitignore）
cp docker-compose.yml.template docker-compose.yml
# 修改其中的 <user> / <pass> 与 <ip:port> 后：
docker compose up -d

# 或直接构建运行
docker build -t buaa-schedule-server .
docker run -d --name buaa-schedule -p 8080:8080 \
  -e XSKB_USER=<账号> -e XSKB_PASS=<密码> buaa-schedule-server
```

`docker-compose.yml.template` 默认使用 CI 发布到 GitHub 容器库的镜像
（`ghcr.io/…:latest`），文件内同时给出注释掉的本地 `build` 方式备用。

## 自动发布（GitHub Actions）

`.github/workflows/release.yml` 在 push 到 `main` 时自动：

1. 依据已有 tag 递增 patch 版本号（`.github/scripts/next-version.sh`）；
2. 打 tag 并创建 GitHub Release；
3. 编译 Windows / Linux (amd64) 的 `server` / `fetch` / `render` 并作为附件上传；
4. 构建并推送容器镜像到 `ghcr.io`。

手动推送 `vX.Y.Z` tag 时以该 tag 为准发布（不再递增）。提交信息可控制递增方式：
`[minor]`、`[major]`，或 `[skip release]` 跳过发布；也可用 `workflow_dispatch` 手动触发。

## 测试

```bash
go test ./...
```

## 数据与隐私说明

- 运行时账号密码只从命令行参数或环境变量读取，**不写入任何代码/配置文件**；
  请勿把真实凭据提交到版本库或分享日志。
- 本项目不包含任何用户隐私数据；内置示例课表（`sample.go`、`testdata/`）为演示用。
- 教务登录系统可能存在风控（例如要求验证码）；纯 HTTP 自动登录在触发风控时
  会明确提示"登录失败"，届时可稍后重试或改用会话复用方式。

## License

见 [LICENSE](LICENSE)。
