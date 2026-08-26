package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"caption-release-workbench/internal/application"
	"caption-release-workbench/internal/httpapi"
	"caption-release-workbench/internal/store"
	"caption-release-workbench/internal/webui"
)

type config struct {
	addr, database string
	selfcheck      bool
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		slog.Error("captionflow 退出", "error", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cfg, err := parseConfig(args)
	if err != nil {
		return err
	}
	if cfg.selfcheck {
		tempDir, err := os.MkdirTemp("", "captionflow-selfcheck-")
		if err != nil {
			return fmt.Errorf("创建自检目录: %w", err)
		}
		defer os.RemoveAll(tempDir)
		cfg.database = filepath.Join(tempDir, "selfcheck.db")
	}
	repository, err := store.Open(cfg.database)
	if err != nil {
		return err
	}
	defer repository.Close()
	service := application.New(repository)
	mux := http.NewServeMux()
	httpapi.New(service, slog.Default()).Register(mux)
	mux.Handle("/", webui.NewHandler())
	listener, err := net.Listen("tcp", cfg.addr)
	if err != nil {
		return fmt.Errorf("监听 %s: %w", cfg.addr, err)
	}
	server := &http.Server{Handler: httpapi.Middleware(securityHeaders(mux), slog.Default()), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 20 * time.Second, IdleTimeout: 60 * time.Second}
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- server.Serve(listener) }()
	actualAddr := listener.Addr().String()
	if cfg.selfcheck {
		checkErr := selfcheck("http://" + actualAddr)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		shutdownErr := server.Shutdown(shutdownCtx)
		serveErr := <-serveErrors
		if checkErr != nil {
			return checkErr
		}
		if shutdownErr != nil {
			return shutdownErr
		}
		if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			return serveErr
		}
		fmt.Println("captionflow 自检通过：完整字幕审校发布流程已验证")
		return nil
	}
	slog.Info("captionflow 已启动", "addr", actualAddr, "database", cfg.database)
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	select {
	case err := <-serveErrors:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-signalCtx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("关闭 HTTP 服务: %w", err)
		}
		err := <-serveErrors
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

func parseConfig(args []string) (config, error) {
	defaultAddr := "127.0.0.1:19081"
	if rawPort := strings.TrimSpace(os.Getenv("PORT")); rawPort != "" {
		port, err := strconv.Atoi(rawPort)
		if err != nil || port < 1 || port > 65535 {
			return config{}, fmt.Errorf("PORT 必须是 1 到 65535 的端口号")
		}
		defaultAddr = net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	}
	set := flag.NewFlagSet("captionflow", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	addr := set.String("addr", defaultAddr, "HTTP 回环监听地址")
	database := set.String("db", "captionflow.db", "SQLite 数据库路径")
	selfcheckFlag := set.Bool("selfcheck", false, "执行完整流程自检后退出")
	if err := set.Parse(args); err != nil {
		return config{}, err
	}
	if set.NArg() != 0 {
		return config{}, fmt.Errorf("存在无法识别的位置参数")
	}
	if err := validateAddress(*addr); err != nil {
		return config{}, err
	}
	if strings.TrimSpace(*database) == "" {
		return config{}, fmt.Errorf("数据库路径不能为空")
	}
	return config{addr: *addr, database: *database, selfcheck: *selfcheckFlag}, nil
}

func validateAddress(addr string) error {
	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("-addr 必须是 host:port：%w", err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("-addr 端口必须在 1 到 65535 之间")
	}
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return fmt.Errorf("-addr 仅允许回环地址，收到 %q", host)
	}
	return nil
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

type selfcheckClient struct {
	base   string
	client *http.Client
}

func selfcheck(base string) error {
	c := &selfcheckClient{base: base, client: &http.Client{Timeout: 5 * time.Second}}
	if _, err := c.get("/api/readiness"); err != nil {
		return fmt.Errorf("readiness 自检: %w", err)
	}
	projectID := "selfcheck-project"
	project, err := c.write(http.MethodPost, "/api/projects", map[string]any{
		"request_id": "selfcheck-create", "id": projectID, "title": "晚间新闻无障碍版",
		"duration_ms": 60000, "language": "zh-CN", "media_checksum": "82dd03875d4bbd090ef31cf8a745b11dd2fe9703d5d58b69b3f73ff145399055",
		"style_profile": "公共广播字幕规范 v1", "assignee": "制作员甲", "actor": "制作员甲",
	})
	if err != nil {
		return fmt.Errorf("建档自检: %w", err)
	}
	revision := project.Revision
	cues := []map[string]any{
		{"id": "cue-1", "start_ms": 0, "end_ms": 2500, "speaker": "主播", "text": "各位听众，晚上好。", "sound_description": ""},
		{"id": "cue-2", "start_ms": 2600, "end_ms": 5200, "speaker": "记者", "text": "这里是本台晚间新闻。", "sound_description": "[片头音乐渐弱]"},
	}
	project, err = c.write(http.MethodPut, "/api/projects/"+projectID+"/cues", selfcheckMeta("save-cues", revision, "制作员甲", map[string]any{"cues": cues}))
	if err != nil {
		return fmt.Errorf("字幕保存自检: %w", err)
	}
	revision = project.Revision
	if _, err := c.writeRaw(http.MethodPost, "/api/projects/"+projectID+"/cues/merge-preview", map[string]any{"cue_ids": []string{"cue-1", "cue-2"}, "expected_revision": revision - 1}); err == nil {
		return fmt.Errorf("过期合并预览应返回修订冲突")
	}
	previewBytes, err := c.writeRaw(http.MethodPost, "/api/projects/"+projectID+"/cues/merge-preview", map[string]any{"cue_ids": []string{"cue-1", "cue-2"}, "expected_revision": revision})
	if err != nil {
		return fmt.Errorf("合并预览自检: %w", err)
	}
	var mergePreview struct {
		ProjectRevision   int64  `json:"project_revision"`
		ConfirmationToken string `json:"confirmation_token"`
		SpeakerConflict   bool   `json:"speaker_conflict"`
	}
	if err := json.Unmarshal(previewBytes, &mergePreview); err != nil || !mergePreview.SpeakerConflict {
		return fmt.Errorf("合并预览冲突标记无效")
	}
	mergeBody := selfcheckMeta("merge-cues", revision, "制作员甲", map[string]any{"cue_ids": []string{"cue-1", "cue-2"}, "preview_revision": mergePreview.ProjectRevision, "confirmation_token": mergePreview.ConfirmationToken, "merged_speaker": "主播与记者"})
	project, err = c.write(http.MethodPost, "/api/projects/"+projectID+"/cues/merge", mergeBody)
	if err != nil {
		return fmt.Errorf("合并确认自检: %w", err)
	}
	if _, err := c.write(http.MethodPost, "/api/projects/"+projectID+"/cues/merge", mergeBody); err != nil {
		return fmt.Errorf("合并幂等重放自检: %w", err)
	}
	revision = project.Revision
	project, err = c.write(http.MethodPost, "/api/projects/"+projectID+"/checks", selfcheckMeta("checks-1", revision, "制作员甲", nil))
	if err != nil {
		return fmt.Errorf("规则检查自检: %w", err)
	}
	revision = project.Revision
	project, err = c.write(http.MethodPost, "/api/projects/"+projectID+"/submit-review", selfcheckMeta("submit-review", revision, "制作员甲", nil))
	if err != nil {
		return fmt.Errorf("提交审校自检: %w", err)
	}
	revision = project.Revision
	project, err = c.write(http.MethodPost, "/api/projects/"+projectID+"/findings", selfcheckMeta("add-finding", revision, "审校员乙", map[string]any{"finding": map[string]any{"id": "finding-1", "cue_id": "cue-1", "category": "accuracy", "severity": "major", "description": "节目名称需要与素材口播一致", "reported_by": "审校员乙"}}))
	if err != nil {
		return fmt.Errorf("登记问题自检: %w", err)
	}
	revision = project.Revision
	project, err = c.write(http.MethodPost, "/api/projects/"+projectID+"/review-decision", selfcheckMeta("return-changes", revision, "审校员乙", map[string]any{"approved": false}))
	if err != nil {
		return fmt.Errorf("退回整改自检: %w", err)
	}
	revision = project.Revision
	cues[0]["text"] = "各位听众，晚上好。这里是公共广播晚间新闻。"
	cues = cues[:1]
	project, err = c.write(http.MethodPut, "/api/projects/"+projectID+"/cues", selfcheckMeta("revise-cues", revision, "制作员甲", map[string]any{"cues": cues}))
	if err != nil {
		return fmt.Errorf("字幕修订自检: %w", err)
	}
	revision = project.Revision
	project, err = c.write(http.MethodPost, "/api/projects/"+projectID+"/checks", selfcheckMeta("checks-2", revision, "制作员甲", nil))
	if err != nil {
		return fmt.Errorf("复查规则自检: %w", err)
	}
	revision = project.Revision
	project, err = c.write(http.MethodPost, "/api/projects/"+projectID+"/remediate", selfcheckMeta("remediate", revision, "制作员甲", map[string]any{"finding_id": "finding-1", "resolution_note": "已按素材口播补全节目名称"}))
	if err != nil {
		return fmt.Errorf("整改记录自检: %w", err)
	}
	revision = project.Revision
	project, err = c.write(http.MethodPost, "/api/projects/"+projectID+"/checks", selfcheckMeta("checks-reverification", revision, "制作员甲", nil))
	if err != nil {
		return fmt.Errorf("复验提交前规则检查自检: %w", err)
	}
	revision = project.Revision
	project, err = c.write(http.MethodPost, "/api/projects/"+projectID+"/submit-reverification", selfcheckMeta("submit-reverify", revision, "制作员甲", nil))
	if err != nil {
		return fmt.Errorf("提交复验自检: %w", err)
	}
	revision = project.Revision
	project, err = c.write(http.MethodPost, "/api/projects/"+projectID+"/verify-finding", selfcheckMeta("verify-finding", revision, "审校员乙", map[string]any{"finding_id": "finding-1", "resolved": true}))
	if err != nil {
		return fmt.Errorf("问题复验自检: %w", err)
	}
	revision = project.Revision
	project, err = c.write(http.MethodPost, "/api/projects/"+projectID+"/complete-reverification", selfcheckMeta("complete-reverify", revision, "审校员乙", nil))
	if err != nil {
		return fmt.Errorf("完成复验自检: %w", err)
	}
	revision = project.Revision
	project, err = c.write(http.MethodPost, "/api/projects/"+projectID+"/checks", selfcheckMeta("checks-release", revision, "发布负责人丙", nil))
	if err != nil {
		return fmt.Errorf("发布前规则检查自检: %w", err)
	}
	revision = project.Revision
	previewData, err := c.get("/api/projects/" + projectID + "/release-preview")
	if err != nil {
		return fmt.Errorf("发布预检自检: %w", err)
	}
	var preview struct {
		CurrentRevision   int64             `json:"current_revision"`
		CaptionChecksum   string            `json:"caption_checksum"`
		ConfirmationToken string            `json:"confirmation_token"`
		Blockers          []json.RawMessage `json:"blockers"`
	}
	if err := json.Unmarshal(previewData, &preview); err != nil || len(preview.Blockers) != 0 {
		return fmt.Errorf("发布预检结果无效")
	}
	project, err = c.write(http.MethodPost, "/api/projects/"+projectID+"/approve", selfcheckMeta("approve", revision, "发布负责人丙", map[string]any{"approved_by": "发布负责人丙", "preview_revision": preview.CurrentRevision, "caption_checksum": preview.CaptionChecksum, "confirmation_token": preview.ConfirmationToken}))
	if err != nil {
		return fmt.Errorf("批准发布自检: %w", err)
	}
	if project.Status != "released" || project.Manifest == nil || len(project.Manifest.CaptionChecksum) != 64 {
		return fmt.Errorf("发布结果未冻结或校验值无效")
	}
	if _, err := c.get("/api/projects/" + projectID + "/manifest"); err != nil {
		return fmt.Errorf("发布清单查询自检: %w", err)
	}
	audit, err := c.get("/api/projects/" + projectID + "/audit?limit=100")
	if err != nil {
		return fmt.Errorf("审计查询自检: %w", err)
	}
	var envelope struct {
		Events []json.RawMessage `json:"events"`
	}
	if err := json.Unmarshal(audit, &envelope); err != nil || len(envelope.Events) < 12 {
		return fmt.Errorf("审计时间线不完整")
	}
	return nil
}

type projectResponse struct {
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
	Status   string `json:"status"`
	Manifest *struct {
		CaptionChecksum string `json:"caption_checksum"`
	} `json:"manifest"`
}

func (c *selfcheckClient) write(method, path string, body any) (*projectResponse, error) {
	data, err := c.writeRaw(method, path, body)
	if err != nil {
		return nil, err
	}
	var envelope struct {
		Project *projectResponse `json:"project"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, err
	}
	if envelope.Project == nil {
		return nil, fmt.Errorf("响应缺少 project")
	}
	return envelope.Project, nil
}

func (c *selfcheckClient) writeRaw(method, path string, body any) ([]byte, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(method, c.base+path, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	responseData, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(responseData)))
	}
	return responseData, nil
}

func (c *selfcheckClient) get(path string) ([]byte, error) {
	resp, err := c.client.Get(c.base + path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	return data, nil
}

func selfcheckMeta(requestID string, revision int64, actor string, extra map[string]any) map[string]any {
	result := map[string]any{"request_id": requestID, "expected_revision": revision, "actor": actor}
	for key, value := range extra {
		result[key] = value
	}
	return result
}
