package documents

import "testing"

func TestSanitizeFilename(t *testing.T) {
	cases := map[string]string{
		"../../etc/passwd":    "passwd",
		`C:\Users\me\cv.pdf`:  "cv.pdf",
		"cv\r\nX-Evil: 1.pdf": "cvX-Evil: 1.pdf",
		`my "best" cv.pdf`:    "my best cv.pdf",
		"":                    "document",
		"   ":                 "document",
	}
	for in, want := range cases {
		if got := SanitizeFilename(in); got != want {
			t.Errorf("SanitizeFilename(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDetectTypeRejectsUnknown(t *testing.T) {
	if _, err := DetectType([]byte("<html><script>"), "cv.pdf"); err == nil {
		t.Fatal("html accepted")
	}
	if m, err := DetectType([]byte("%PDF-1.7"), "CV.PDF"); err != nil || m != MimePDF {
		t.Fatalf("pdf: %v %v", m, err)
	}
}
