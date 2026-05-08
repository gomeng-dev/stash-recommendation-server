package scoring

import "testing"

func TestHybridV3LiteWeightsUseTunedVisualSignal(t *testing.T) {
	cases := map[string]struct {
		weights    Weights
		wantVisual float64
	}{
		"metadata-rich": {weights: metadataRichWeights, wantVisual: 0.34},
		"tag-sparse":    {weights: tagSparseWeights, wantVisual: 0.46},
	}
	for name, tc := range cases {
		weights := tc.weights
		sum := weights.Tag + weights.Filename + weights.Visual + weights.PHash + weights.Duration + weights.Resolution + weights.Behavior
		if sum < 0.999999 || sum > 1.000001 {
			t.Fatalf("%s weights should sum to 1, got %v", name, sum)
		}
		if weights.Visual != tc.wantVisual {
			t.Fatalf("%s visual weight mismatch: got %v want %v", name, weights.Visual, tc.wantVisual)
		}
	}
}

func TestNormalizePHashAndDistance(t *testing.T) {
	if got := NormalizePHash(" 0xABCDEF0123456789 "); got != "abcdef0123456789" {
		t.Fatalf("NormalizePHash mismatch: %q", got)
	}
	if got := NormalizePHash("f"); got != "" {
		t.Fatalf("short phash should be ignored, got %q", got)
	}
	if got := NormalizePHash("zzzzef0123456789"); got != "" {
		t.Fatalf("malformed phash should be ignored, got %q", got)
	}
	distance, ok := PHashDistance("0000000000000000", "000000000000000f")
	if !ok || distance != 4 {
		t.Fatalf("distance mismatch: got %d ok=%v", distance, ok)
	}
	if sim := PHashSimilarity("0000000000000000", "000000000000000f"); sim != 0.9375 {
		t.Fatalf("similarity mismatch: %v", sim)
	}
}

func TestTokenizeFileNameAndSimilarity(t *testing.T) {
	got := TokenizeFileName("Final_MY-Series_scene_001_1080p.mp4")
	want := []string{"my", "series"}
	if len(got) != len(want) {
		t.Fatalf("tokens length mismatch: got %#v want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("tokens mismatch: got %#v want %#v", got, want)
		}
	}
	if sim := FilenameSimilarity("alpha beta.mp4", "alpha gamma.mp4"); sim < 0.32 || sim > 0.34 {
		t.Fatalf("filename similarity mismatch: %v", sim)
	}
}

func TestMetadataSimilarities(t *testing.T) {
	if sim := TagSimilarity([]string{"Action", "Drama", "action"}, []string{"drama", "comedy"}); sim < 0.32 || sim > 0.34 {
		t.Fatalf("tag similarity mismatch: %v", sim)
	}
	if sim := DurationSimilarity(100, 90); sim != 0.9 {
		t.Fatalf("duration similarity mismatch: %v", sim)
	}
	left := Scene{Width: 1920, Height: 1080}
	right := Scene{Width: 1280, Height: 720}
	if sim := ResolutionSimilarity(left, right); sim != 0.5 {
		t.Fatalf("adjacent resolution mismatch: %v", sim)
	}
}

func TestHybridV3LiteScoreUsesPHashWithoutExposingRawValue(t *testing.T) {
	source := Scene{ID: "source", FileName: "alpha beta.mp4", Tags: []string{"tag-a", "tag-b"}, DurationSeconds: 100, Width: 1920, Height: 1080, PHash: "0000000000000000"}
	candidate := Scene{ID: "candidate", FileName: "alpha gamma.mp4", Tags: []string{"tag-a", "tag-c"}, DurationSeconds: 95, Width: 1920, Height: 1080, PHash: "0000000000000003"}
	result, ok := HybridV3LiteScore(source, candidate)
	if !ok {
		t.Fatal("expected score")
	}
	if result.Score <= 0.25 || result.Score > 1 {
		t.Fatalf("unexpected score: %#v", result)
	}
	if result.Breakdown.PHash <= 0.9 {
		t.Fatalf("expected strong phash component: %#v", result.Breakdown)
	}
	for _, reason := range result.Reasons {
		if reason == "0000000000000003" {
			t.Fatal("raw pHash leaked in reasons")
		}
	}
}

func TestHasLiteCandidateSignal(t *testing.T) {
	source := Scene{ID: "source", DurationSeconds: 120, Width: 1920, Height: 1080, PHash: "0000000000000000"}
	candidate := Scene{ID: "candidate", DurationSeconds: 119, Width: 1920, Height: 1080, PHash: "000000000000ffff"}
	if !HasLiteCandidateSignal(source, candidate) {
		t.Fatal("expected duration/resolution signal")
	}
	far := Scene{ID: "far", DurationSeconds: 30, Width: 640, Height: 360, PHash: "ffffffffffffffff"}
	if HasLiteCandidateSignal(source, far) {
		t.Fatal("unexpected signal for far candidate")
	}
}
