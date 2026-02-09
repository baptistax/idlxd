package instagram

import "testing"

func TestNormalizeImageURLToJPEG_WebPToJPG_PreservesQuery(t *testing.T) {
	in := "https://example.cdn/abc/def/image.webp?x=1&y=2"
	out := NormalizeImageURLToJPEG(in)

	want := "https://example.cdn/abc/def/image.jpg?x=1&y=2"
	if out != want {
		t.Fatalf("unexpected output: got %q want %q", out, want)
	}
}

func TestNormalizeImageURLToJPEG_AlreadyJPEG_Unchanged(t *testing.T) {
	in := "https://example.cdn/abc/def/image.jpeg?x=1"
	out := NormalizeImageURLToJPEG(in)
	if out != in {
		t.Fatalf("unexpected output: got %q want %q", out, in)
	}
}
