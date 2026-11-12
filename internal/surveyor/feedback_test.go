package surveyor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/componentproto"
)

// pageText stands for page content an error could quote. It must never reach a
// candidate, whatever the error looks like.
const pageText = "the quarterly numbers of kb-secret are 4242"

type answerTools struct {
	text    string
	isError bool
	err     error
}

func (a answerTools) ListTools() ([]client.ToolInfo, error) { return nil, nil }
func (a answerTools) CallTool(string, any) (client.ToolOutput, error) {
	return client.ToolOutput{Text: a.text, IsError: a.isError}, a.err
}

func pendingJSON(t *testing.T, l *CandidateLog) string {
	t.Helper()
	got, _ := l.pending(true)
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestObserveCalls_RecordsUnexpectedErrorsAsCodesOnly(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, text string
		want       string // the recorded code, "" for nothing
	}{
		{"internal error", client.InternalErrorPrefix + ": tool concept_read panicked: " + pageText, internalErrorCode},
		{"unexpected code", "bad_graph: " + pageText, "bad_graph"},
		{"refused", "refused: " + pageText, ""},
		{"forbidden", "forbidden: " + pageText, ""},
		{"not found", "not_found: " + pageText, ""},
		{"stale write is retried", "stale_write: " + pageText, ""},
		{"read only grant", "read_only_profile: " + pageText, ""},
		{"a tool prefixing its own refusal", "concept_read: " + pageText, ""},
		{"a batch prefixing its sub-operation", "concept_move: " + pageText, ""},
		{"a check prefixing its refusal", "validate: " + pageText, ""},
		{"a gate prefixing its refusal", "commit_gate: " + pageText, ""},
		{"validation text has no code", "invalid params: " + pageText, ""},
		{"a sentence", pageText, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			l := NewCandidateLog(nil)
			out, err := ObserveCalls(answerTools{text: tc.text, isError: true}, l, "kb-a").CallTool("concept_read", nil)
			if err != nil || !out.IsError || out.Text != tc.text {
				t.Fatalf("the task must see the answer unchanged: %+v %v", out, err)
			}
			got := pendingJSON(t, l)
			if tc.want == "" {
				if got != "[]" {
					t.Fatalf("recorded %s, want nothing", got)
				}
				return
			}
			if !strings.Contains(got, `"code":"`+tc.want+`"`) || !strings.Contains(got, `"tool":"concept_read"`) || !strings.Contains(got, `"kb":"kb-a"`) {
				t.Fatalf("recorded %s, want code %s", got, tc.want)
			}
			if strings.Contains(got, "quarterly") || strings.Contains(got, "4242") || strings.Contains(got, "panicked") {
				t.Fatalf("a candidate carries text from the answer: %s", got)
			}
		})
	}
}

func TestObserveCalls_SuccessAndTransportErrorRecordNothing(t *testing.T) {
	t.Parallel()
	l := NewCandidateLog(nil)
	ObserveCalls(answerTools{text: "bad_graph: x"}, l, "kb-a").CallTool("concept_read", nil)
	ObserveCalls(answerTools{err: &client.RemoteError{HTTPStatus: 502}}, l, "kb-a").CallTool("concept_read", nil)
	if got := pendingJSON(t, l); got != "[]" {
		t.Fatalf("recorded %s", got)
	}
}

func TestCandidateLog_DedupesBoundsAndKeepsToTheVocabulary(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	l := NewCandidateLog(func() time.Time { return now })
	l.Note(componentproto.CandidateToolError, "concept_read", "kb-a", "bad_graph")
	now = now.Add(90 * time.Second)
	l.Note(componentproto.CandidateToolError, "concept_read", "kb-a", "bad_graph")
	got, _ := l.pending(false)
	if len(got) != 1 || got[0].Count != 2 || got[0].FirstAgeSeconds != 90 || got[0].LastAgeSeconds != 0 {
		t.Fatalf("dedup: %+v (ages are relative to the post, never a time)", got)
	}
	// Outside the vocabulary: dropped, or "unknown" for a code.
	l.Note("made_up", "concept_read", "kb-a", "x")
	l.Note(componentproto.CandidateToolError, "Not A Tool!", "kb-a", "x")
	l.Note(componentproto.CandidateToolError, "concept_read", "kb-b", pageText)
	var nilLog *CandidateLog
	nilLog.Note(componentproto.CandidateToolError, "concept_read", "kb-a", "x") // a nil log records nothing
	all := pendingJSON(t, l)
	if strings.Contains(all, "made_up") || strings.Contains(all, "Not A Tool") || strings.Contains(all, "quarterly") || !strings.Contains(all, `"code":"unknown"`) {
		t.Fatalf("vocabulary: %s", all)
	}
	// Bounds: the log keeps componentproto.MaxCandidatesPerSurveyor, a post
	// carries componentproto.MaxCandidatesPerPost, the oldest sighting goes first.
	for i := 0; i < componentproto.MaxCandidatesPerSurveyor+10; i++ {
		now = now.Add(time.Second)
		l.Note(componentproto.CandidateToolError, "concept_read", "kb-a", fmt.Sprintf("code_%03d", i))
	}
	l.mu.Lock()
	n := len(l.by)
	l.mu.Unlock()
	if n != componentproto.MaxCandidatesPerSurveyor {
		t.Fatalf("the log holds %d, want %d", n, componentproto.MaxCandidatesPerSurveyor)
	}
	post, _ := l.pending(true)
	if len(post) != componentproto.MaxCandidatesPerPost || post[0].Code != fmt.Sprintf("code_%03d", componentproto.MaxCandidatesPerSurveyor+9) {
		t.Fatalf("post of %d starting with %s, want %d newest first", len(post), post[0].Code, componentproto.MaxCandidatesPerPost)
	}
}

func TestBeaterCandidatesRideTheRunsReportUntilAcknowledged(t *testing.T) {
	now := time.Now()
	api := &stubAPI{answer: &client.HeartbeatAnswer{}}
	b := newBeater(api, &now)
	b.Candidates = NewCandidateLog(func() time.Time { return now })
	b.Beat(context.Background(), State{Run: StateIdle})
	if len(api.runPosts) != 0 {
		t.Fatalf("nothing noted yet, posts %v", api.runPosts)
	}
	b.Candidates.Note(componentproto.CandidateToolError, "concept_read", "kb-a", internalErrorCode)
	now = now.Add(30 * time.Second)
	b.send()
	if len(api.runPosts) != 1 {
		t.Fatalf("posts = %d, want the candidate sent without a run", len(api.runPosts))
	}
	c := api.runPosts[0]["report_candidates"].([]any)[0].(map[string]any)
	if c["kind"] != "tool_error" || c["tool"] != "concept_read" || c["code"] != internalErrorCode || c["kb"] != "kb-a" || c["count"] != float64(1) || c["last_age_seconds"] != float64(30) {
		t.Fatalf("candidate = %v", c)
	}
	for k := range c {
		if strings.Contains(k, "_at") {
			t.Fatalf("a time went to the server: %s", k)
		}
	}
	b.send() // acknowledged: not sent again
	if len(api.runPosts) != 1 {
		t.Fatalf("an acknowledged candidate was sent again: %d posts", len(api.runPosts))
	}
	b.Candidates.Note(componentproto.CandidateToolError, "concept_read", "kb-a", internalErrorCode)
	b.send() // seen again: the new count goes
	if len(api.runPosts) != 2 || api.runPosts[1]["report_candidates"].([]any)[0].(map[string]any)["count"] != float64(2) {
		t.Fatalf("posts = %v", api.runPosts)
	}
	api.mu.Lock()
	api.answer.RunsWanted = true
	api.mu.Unlock()
	b.send() // a restarted server asks for a refill
	if len(api.runPosts) != 3 || len(api.runPosts[2]["report_candidates"].([]any)) != 1 {
		t.Fatalf("refill: %v", api.runPosts)
	}
}

func TestBeaterRefusedCandidatesAreKeptAndTriedAgain(t *testing.T) {
	now := time.Now()
	api := &stubAPI{answer: &client.HeartbeatAnswer{}, runsErr: &client.RemoteError{HTTPStatus: 404}}
	b := newBeater(api, &now)
	b.Candidates = NewCandidateLog(func() time.Time { return now })
	b.Beat(context.Background(), State{Run: StateIdle})
	b.Candidates.Note(componentproto.CandidateContractMismatch, "concept_list", "kb-a", "undecodable")
	b.send() // 4xx: a back-off, nothing lost
	api.mu.Lock()
	api.runsErr = nil
	api.mu.Unlock()
	for i := 0; i <= runsBackoffBeats; i++ {
		b.send()
	}
	if len(api.runPosts) != 1 || len(api.runPosts[0]["report_candidates"].([]any)) != 1 {
		t.Fatalf("posts = %v, want the candidate sent once the route answers", api.runPosts)
	}
}

func TestBeaterNotesAProtocolTheServerRefusesOncePerCause(t *testing.T) {
	now := time.Now()
	api := &stubAPI{answer: &client.HeartbeatAnswer{}, beatErr: &client.RemoteError{HTTPStatus: 426, ServerProtocol: 4, ServerMinProtocol: 3}}
	b := newBeater(api, &now)
	b.Candidates = NewCandidateLog(func() time.Time { return now })
	b.send()
	b.send()
	got, _ := b.Candidates.pending(false)
	if len(got) != 1 || got[0].Kind != "protocol_mismatch" || got[0].Count != 1 || got[0].Tool != "" || got[0].KB != "" {
		t.Fatalf("candidates = %+v, want one protocol_mismatch counted once", got)
	}
}

// A protocol the server refuses (426) cannot be posted while it lasts, the runs
// route refuses it too; the candidate is kept and arrives once the beat is accepted.
func TestBeaterProtocolCandidateArrivesWhenTheBeatIsAcceptedAgain(t *testing.T) {
	now := time.Now()
	api := &stubAPI{answer: &client.HeartbeatAnswer{}, beatErr: &client.RemoteError{HTTPStatus: 426, ServerProtocol: 4, ServerMinProtocol: 3}}
	b := newBeater(api, &now)
	b.Candidates = NewCandidateLog(func() time.Time { return now })
	b.send()
	if len(api.runPosts) != 0 {
		t.Fatalf("a refused protocol posted runs: %v", api.runPosts)
	}
	api.mu.Lock()
	api.beatErr = nil
	api.mu.Unlock()
	b.send()
	if len(api.runPosts) != 1 {
		t.Fatalf("posts = %d, want the candidate delivered once the beat is accepted", len(api.runPosts))
	}
	c := api.runPosts[0]["report_candidates"].([]any)[0].(map[string]any)
	if c["kind"] != "protocol_mismatch" || c["code"] != "protocol_refused" {
		t.Fatalf("candidate = %v", c)
	}
}

// An answer two or more protocols below ours skips the idle signal but not the
// runs report: the candidate it raises goes in the same beat.
func TestBeaterServerFarBelowStillPostsItsCandidate(t *testing.T) {
	now := time.Now()
	api := &stubAPI{answer: &client.HeartbeatAnswer{Protocol: 2}}
	b := newBeater(api, &now)
	b.Protocol = 5
	b.Candidates = NewCandidateLog(func() time.Time { return now })
	b.Beat(context.Background(), State{Run: StateIdle})
	if len(api.runPosts) != 1 || api.runPosts[0]["protocol"] != float64(5) {
		t.Fatalf("posts = %v, want one runs post carrying the candidate", api.runPosts)
	}
	c := api.runPosts[0]["report_candidates"].([]any)[0].(map[string]any)
	if c["kind"] != "protocol_mismatch" || c["code"] != "server_behind" {
		t.Fatalf("candidate = %v", c)
	}
}

func TestNoteContract_AnUndecodableResultIsACandidate(t *testing.T) {
	t.Parallel()
	l := NewCandidateLog(nil)
	tools := ObserveCalls(answerTools{text: "{not json " + pageText}, l, "kb-a")
	if _, err := conceptList(tools, map[string]any{}); err == nil {
		t.Fatal("an undecodable list must fail")
	}
	got := pendingJSON(t, l)
	if !strings.Contains(got, `"kind":"contract_mismatch"`) || !strings.Contains(got, `"tool":"concept_list"`) || strings.Contains(got, "quarterly") {
		t.Fatalf("recorded %s", got)
	}
	NoteContract(answerTools{}, "concept_list") // a plain caller: nothing to record, nothing to fail
}

// The caller a task holds is wrapped by the guards of its kind: the log is
// found through every one of them.
func TestNoteContract_LooksThroughTheGuards(t *testing.T) {
	t.Parallel()
	wrap := map[string]func(ToolCaller) ToolCaller{
		"promptGuard": func(in ToolCaller) ToolCaller { return newPromptGuard(in, "t") },
		"readOnly":    func(in ToolCaller) ToolCaller { return readOnly{in} },
		"doctorGuard": func(in ToolCaller) ToolCaller { return &doctorGuard{inner: in} },
		"ingestTools": func(in ToolCaller) ToolCaller { return &ingestTools{inner: in} },
		"two deep":    func(in ToolCaller) ToolCaller { return readOnly{newPromptGuard(in, "t")} },
	}
	for name, w := range wrap {
		l := NewCandidateLog(nil)
		NoteContract(w(ObserveCalls(answerTools{}, l, "kb-a")), "concept_read")
		if got := pendingJSON(t, l); !strings.Contains(got, `"kind":"contract_mismatch"`) {
			t.Errorf("%s hides the log: %s", name, got)
		}
	}
}
