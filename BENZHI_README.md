# BENZHI_README

## 项目说明
- 项目：benzhi-project-b7a4cbb4-3e57-47c6-87a6-7890e93bc2e7
- 项目用途：面向公共广播无障碍制作团队的字幕母版审校与冻结发布工作台，已实现可追溯的完整业务流程、SQLite 事务持久化、同源 JSON API、原生浏览器界面和真实 HTTP 自检。
- Go 工具链：`golang:1.23.0`
- 前端工具链：原生 HTML、CSS 和 JavaScript，由 Go 服务直接提供

## 项目描述
- 项目名称：caption-release-workbench
- 项目介绍：面向公共广播无障碍制作团队的字幕母版审校与发布工作台，将节目素材、时间轴字幕、可访问性标注、审校问题、定向复验和冻结发布纳入一条可追溯的状态流程。
- 项目概述：面向公共广播无障碍制作团队的字幕母版审校与发布工作台，将节目素材、时间轴字幕、可访问性标注、审校问题、定向复验和冻结发布纳入一条可追溯的状态流程。
- 核心工作流：节目素材与制作规则建档后进入字幕制作，完成时间轴和可访问性规则校验后提交审校；审校员登记问题并退回整改，制作员逐项修订并发起定向复验，全部问题通过后由发布负责人批准，系统冻结字幕母版并生成可验证的发布清单。
- 对外接口：由 Go 服务提供原生 HTML、CSS 和 JavaScript 的浏览器工作台，包含节目队列、字幕时间轴编辑、规则检查、问题审校、定向复验和发布清单视图，全部操作通过同源 JSON 接口完成。

## 标准构建、运行和测试命令
进入容器后执行：
cd '/app' && GOTOOLCHAIN=local go build ./...

cd /app && GOTOOLCHAIN=local go run ./cmd/captionflow -addr=127.0.0.1:19081

cd '/app' && GOTOOLCHAIN=local go test ./...

## Docker 构建和进入容器
chmod +x build_benzhi_docker.sh

./build_benzhi_docker.sh benzhi-project-b7a4cbb4-3e57-47c6-87a6-7890e93bc2e7-amd64 linux/amd64

./build_benzhi_docker.sh benzhi-project-b7a4cbb4-3e57-47c6-87a6-7890e93bc2e7-arm64 linux/arm64

docker run -it benzhi-project-b7a4cbb4-3e57-47c6-87a6-7890e93bc2e7-amd64:latest

## 题目验证命令
1. 预期退出码 0：`go test ./...`
2. 预期退出码 0：`go run ./cmd/captionflow -selfcheck -addr=127.0.0.1:19081`
