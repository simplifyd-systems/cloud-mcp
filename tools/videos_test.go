package tools

import (
	"errors"
	"strings"
	"testing"

	cloud "github.com/simplifyd-systems/cloud-go-sdk"
)

func TestInterruptedUploadReportsTheResumeID(t *testing.T) {
	result := interruptedUploadResult("training", &cloud.UploadError{
		VideoSlug:   "vid-42",
		StoredParts: 187,
		PartCount:   214,
		Err:         errors.New("connection reset"),
	})
	if result == nil {
		t.Fatal("an interrupted upload was not recognised")
	}
	output := resultText(t, result)
	for _, want := range []string{"vid-42", "upload_interrupted", "187", "214", "resume=vid-42"} {
		if !strings.Contains(output, want) {
			t.Errorf("result does not mention %q: %s", want, output)
		}
	}
}

func TestAbandonedUploadIsNotOfferedForResume(t *testing.T) {
	// Nothing is left to resume, so the caller must not be told to try.
	if result := interruptedUploadResult("training", &cloud.UploadError{
		VideoSlug: "vid-42", Abandoned: true, Err: errors.New("connection reset"),
	}); result != nil {
		t.Fatalf("an abandoned upload was offered for resume: %s", resultText(t, result))
	}
}

func TestOrdinaryUploadErrorsFallThroughToAPIErr(t *testing.T) {
	if result := interruptedUploadResult("training", errors.New("no such file")); result != nil {
		t.Fatalf("an unrelated error was reported as interrupted: %s", resultText(t, result))
	}
}
