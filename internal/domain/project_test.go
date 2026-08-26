package domain

import (
	"testing"
	"time"
)

func testProject(t *testing.T) *CaptionProject {
	t.Helper()
	p, err := CreateProject(NewProject{ID: "p1", Title: "测试节目", DurationMS: 60000, Language: "zh-CN", MediaChecksum: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", StyleProfile: "规范 v1", Assignee: "制作员"}, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCompleteReviewAndReleaseWorkflow(t *testing.T) {
	now := time.Unix(2, 0)
	p := testProject(t)
	cues := []CaptionCue{{ID: "c1", StartMS: 0, EndMS: 2000, Speaker: "主播", Text: "晚上好"}, {ID: "c2", StartMS: 2100, EndMS: 4500, Speaker: "记者", Text: "这里是新闻", SoundDescription: "[音乐渐弱]"}}
	if err := p.SaveCues(cues, now); err != nil {
		t.Fatal(err)
	}
	p.RunChecks(now)
	if !p.ChecksPassed() {
		t.Fatal("规则应通过")
	}
	if err := p.SubmitReview(now); err != nil {
		t.Fatal(err)
	}
	if err := p.AddFinding(ReviewFinding{ID: "f1", CueID: "c2", Category: "accuracy", Severity: "major", Description: "名称错误", ReportedBy: "审校员"}, now); err != nil {
		t.Fatal(err)
	}
	if err := p.ReviewDecision(false, now); err != nil {
		t.Fatal(err)
	}
	cues[1].Text = "这里是公共广播新闻"
	if err := p.SaveCues(cues, now); err != nil {
		t.Fatal(err)
	}
	p.RunChecks(now)
	if err := p.Remediate("f1", "已按素材修正", now); err != nil {
		t.Fatal(err)
	}
	if p.Findings[0].ResolvedCueRevision != 2 {
		t.Fatalf("整改应关联字幕版本 2，得到 %d", p.Findings[0].ResolvedCueRevision)
	}
	if err := p.SubmitReverification(now); err != nil {
		t.Fatal(err)
	}
	if err := p.VerifyFinding("f1", "审校员", true, now); err != nil {
		t.Fatal(err)
	}
	if err := p.CompleteReverification(now); err != nil {
		t.Fatal(err)
	}
	manifest, err := p.Approve("负责人", "m1", now)
	if err != nil {
		t.Fatal(err)
	}
	if p.Status != StatusReleased || manifest.CueCount != 2 || len(manifest.CaptionChecksum) != 64 {
		t.Fatalf("发布结果无效: %#v", manifest)
	}
	if err := p.SaveCues(cues, now); err == nil {
		t.Fatal("发布后应禁止编辑")
	}
}

func TestTimelineValidation(t *testing.T) {
	p := testProject(t)
	cases := []struct {
		name string
		cues []CaptionCue
	}{
		{"重叠", []CaptionCue{{ID: "a", StartMS: 0, EndMS: 2000, Text: "A"}, {ID: "b", StartMS: 1900, EndMS: 3000, Text: "B"}}},
		{"越界", []CaptionCue{{ID: "a", StartMS: 0, EndMS: 70000, Text: "A"}}},
		{"空内容", []CaptionCue{{ID: "a", StartMS: 0, EndMS: 2000}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := p.SaveCues(tc.cues, time.Now()); err == nil {
				t.Fatal("预期校验失败")
			}
		})
	}
}

func TestStableCaptionChecksum(t *testing.T) {
	p := testProject(t)
	if err := p.SaveCues([]CaptionCue{{ID: "c1", StartMS: 0, EndMS: 2000, Speaker: " 主播 ", Text: " 晚上好 "}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	first := p.CaptionChecksum()
	p.Cues[0].ID = "other-id"
	p.Cues[0].CueRevision = 99
	if second := p.CaptionChecksum(); first != second {
		t.Fatalf("业务无关字段不应改变校验值: %s != %s", first, second)
	}
}
