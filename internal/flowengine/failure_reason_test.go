package flowengine

import (
	"database/sql"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"sophus/internal/repo"
)

func TestNodeFailureReason(t *testing.T) {
	if got := nodeFailureReason(ExecutionContext{"transcriptionError": " nenhuma mídia recebida foi encontrada "}); got != "nenhuma mídia recebida foi encontrada" {
		t.Fatalf("transcription reason = %q", got)
	}
	if got := nodeFailureReason(ExecutionContext{"aiError": nil, "httpError": "o servidor respondeu com status 502"}); got != "o servidor respondeu com status 502" {
		t.Fatalf("http reason = %q", got)
	}
	if got := nodeFailureReason(ExecutionContext{"transcriptionError": ""}); got != "motivo não informado pelo bloco" {
		t.Fatalf("missing reason = %q", got)
	}
	if got := httpFailureReason(map[string]interface{}{"error": true, "message": "timeout de conexão"}); got != "timeout de conexão" {
		t.Fatalf("transport failure = %q", got)
	}
	if got := httpFailureReason(map[string]interface{}{"status": 404}); got != "o servidor respondeu com status 404" {
		t.Fatalf("status failure = %q", got)
	}
	if failureNodeLabel(NodeMediaTranscription) != "Transcrever Mídia" || failureNodeLabel("custom") != "custom" {
		t.Fatal("unexpected block labels")
	}
}

// Every path that leaves a block through "failure" must say why: a missing
// reason is what made a failed transcription impossible to diagnose.
func TestEveryFailureExitRecordsAReason(t *testing.T) {
	source, err := os.ReadFile("engine.go")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(source), "\n")
	checked := 0
	for index, line := range lines {
		if !strings.Contains(line, `OutputHandle: "failure"`) {
			continue
		}
		checked++
		window := strings.Join(lines[index:min(len(lines), index+3)], "\n")
		found := false
		for _, key := range nodeFailureReasonKeys {
			if strings.Contains(window, `"`+key+`"`) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("engine.go:%d leaves through failure without a reason key (%s)", index+1, strings.Join(nodeFailureReasonKeys, ", "))
		}
	}
	if checked < 10 {
		t.Fatalf("only %d failure exits found; the check no longer matches the engine code", checked)
	}
}

// The dispatcher stores "message:<id>"; looking the message up with the
// prefix never matched, so every triggered transcription found no media.
func TestIncomingMessageIDDropsDispatcherPrefix(t *testing.T) {
	for key, want := range map[string]string{
		"message:TESTMSG0001": "TESTMSG0001",
		"button:TESTBTN0001":  "",
		" TESTMSG0002 ":       "TESTMSG0002",
		"":                    "",
	} {
		if got := incomingMessageID(ExecutionContext{"messageId": key}); got != want {
			t.Errorf("messageId %q -> %q, want %q", key, got, want)
		}
	}
	if got := incomingMessageID(ExecutionContext{}); got != "" {
		t.Fatalf("missing messageId -> %q", got)
	}
}

func TestAwaitIncomingMediaWaitsForDownload(t *testing.T) {
	calls := 0
	downloading := func() (repo.MessageData, error) {
		calls++
		if calls < 3 {
			return repo.MessageData{MessageId: "m", MediaType: "audio"}, nil
		}
		return repo.MessageData{MessageId: "m", MediaType: "audio", MediaPath: "messages/audio.ogg"}, nil
	}
	attachment, reason := awaitIncomingMedia(downloading, time.Second, time.Millisecond)
	if reason != "" || attachment.MediaPath != "messages/audio.ogg" || calls != 3 {
		t.Fatalf("download not awaited: reason=%q path=%q calls=%d", reason, attachment.MediaPath, calls)
	}
	for name, test := range map[string]struct {
		lookup func() (repo.MessageData, error)
		reason string
	}{
		"text message":  {func() (repo.MessageData, error) { return repo.MessageData{}, sql.ErrNoRows }, "não contém mídia"},
		"lookup error":  {func() (repo.MessageData, error) { return repo.MessageData{}, errors.New("db down") }, "não foi possível consultar"},
		"deleted media": {func() (repo.MessageData, error) { return repo.MessageData{IsDeleted: true}, nil }, "apagada"},
		"never arrives": {func() (repo.MessageData, error) { return repo.MessageData{MediaType: "audio"}, nil }, "download da mídia recebida não terminou"},
	} {
		started := time.Now()
		_, reason := awaitIncomingMedia(test.lookup, 30*time.Millisecond, 5*time.Millisecond)
		if !strings.Contains(reason, test.reason) {
			t.Errorf("%s: reason %q", name, reason)
		}
		if time.Since(started) > time.Second {
			t.Errorf("%s: waited too long", name)
		}
	}
}

func TestStoredMediaContentTypeDoesNotDependOnHost(t *testing.T) {
	for path, want := range map[string]string{
		"3/abc.ogg": "audio/ogg", "3/abc.OGG": "audio/ogg", "3/abc.mp4": "video/mp4", "3/abc.m4a": "audio/mp4",
		"3/abc.weba": "audio/webm", "3/abc.jpg": "image/jpeg", "3/abc.pdf": "application/pdf", "3/abc.bin": "application/octet-stream",
	} {
		if got := storedMediaContentType(path); got != want {
			t.Errorf("%s -> %q, want %q", path, got, want)
		}
	}
}
