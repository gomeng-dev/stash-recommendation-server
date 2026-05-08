package scoring

import (
	"math"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	ModelVersionHybridV3Lite = "hybrid-v3-lite"
	phashBits                = 64
)

var tokenRe = regexp.MustCompile(`[\p{L}\p{N}]+`)

var stopWords = map[string]struct{}{
	"scene": {}, "final": {}, "copy": {}, "sample": {}, "video": {}, "movie": {}, "clip": {},
	"version": {}, "draft": {}, "edit": {}, "hd": {}, "fhd": {}, "uhd": {}, "sd": {},
	"4k": {}, "8k": {}, "720p": {}, "1080p": {}, "1440p": {}, "2160p": {}, "web": {}, "www": {},
}

type Scene struct {
	ID              string
	Title           string
	FileName        string
	DurationSeconds float64
	Width           int
	Height          int
	Tags            []string
	ThumbnailURL    string
	SpriteImageURL  string
	SpriteVTTURL    string
	PHash           string
	Rating100       int
	PlayCount       int
	StashUpdatedAt  string
}

type Weights struct {
	Tag        float64 `json:"tag"`
	Filename   float64 `json:"filename"`
	Visual     float64 `json:"visual"`
	PHash      float64 `json:"phash"`
	Duration   float64 `json:"duration"`
	Resolution float64 `json:"resolution"`
	Behavior   float64 `json:"behavior"`
}

type Breakdown struct {
	Tag        float64 `json:"tag"`
	Filename   float64 `json:"filename"`
	Visual     float64 `json:"visual"`
	PHash      float64 `json:"phash"`
	Duration   float64 `json:"duration"`
	Resolution float64 `json:"resolution"`
	Behavior   float64 `json:"behavior"`
}

type Result struct {
	SceneID   string
	Score     float64
	Reasons   []string
	Breakdown Breakdown
	Weights   Weights
}

var metadataRichWeights = Weights{Tag: 0.264, Filename: 0.185, Visual: 0.34, PHash: 0.066, Duration: 0.092, Resolution: 0.040, Behavior: 0.013}
var tagSparseWeights = Weights{Tag: 0.119, Filename: 0.194, Visual: 0.46, PHash: 0.076, Duration: 0.097, Resolution: 0.032, Behavior: 0.022}

func NormalizePHash(value string) string {
	normalized := strings.TrimSpace(strings.ToLower(value))
	normalized = strings.TrimPrefix(normalized, "0x")
	if len(normalized) != 16 {
		return ""
	}
	for _, r := range normalized {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return ""
		}
	}
	return normalized
}

func PHashDistance(left, right string) (int, bool) {
	l := NormalizePHash(left)
	r := NormalizePHash(right)
	if l == "" || r == "" {
		return 0, false
	}
	lv, err := strconv.ParseUint(l, 16, 64)
	if err != nil {
		return 0, false
	}
	rv, err := strconv.ParseUint(r, 16, 64)
	if err != nil {
		return 0, false
	}
	return bitCount64(lv ^ rv), true
}

func PHashSimilarity(left, right string) float64 {
	distance, ok := PHashDistance(left, right)
	if !ok {
		return 0
	}
	return 1 - float64(distance)/phashBits
}

func bitCount64(value uint64) int {
	count := 0
	for value > 0 {
		value &= value - 1
		count++
	}
	return count
}

func TokenizeFileName(fileName string) []string {
	base := filepath.Base(strings.TrimSpace(fileName))
	if ext := filepath.Ext(base); len(ext) >= 3 && len(ext) <= 6 {
		base = strings.TrimSuffix(base, ext)
	}
	raw := tokenRe.FindAllString(strings.ToLower(base), -1)
	seen := map[string]struct{}{}
	var tokens []string
	for _, token := range raw {
		if len([]rune(token)) < 2 {
			continue
		}
		if isDigits(token) {
			continue
		}
		if _, stop := stopWords[token]; stop {
			continue
		}
		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		tokens = append(tokens, token)
	}
	return tokens
}

func FilenameSimilarity(left, right string) float64 {
	leftTokens := TokenizeFileName(left)
	rightTokens := TokenizeFileName(right)
	if len(leftTokens) == 0 || len(rightTokens) == 0 {
		return 0
	}
	rightSet := stringSet(rightTokens)
	union := stringSet(append(append([]string{}, leftTokens...), rightTokens...))
	intersection := 0
	for _, token := range leftTokens {
		if _, ok := rightSet[token]; ok {
			intersection++
		}
	}
	if len(union) == 0 {
		return 0
	}
	return float64(intersection) / float64(len(union))
}

func NormalizeTags(tags []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, tag := range tags {
		value := strings.TrimSpace(strings.ToLower(tag))
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func TagSimilarity(left, right []string) float64 {
	leftSet := stringSet(NormalizeTags(left))
	rightSet := stringSet(NormalizeTags(right))
	if len(leftSet) == 0 || len(rightSet) == 0 {
		return 0
	}
	union := map[string]struct{}{}
	intersection := 0
	for tag := range leftSet {
		union[tag] = struct{}{}
		if _, ok := rightSet[tag]; ok {
			intersection++
		}
	}
	for tag := range rightSet {
		union[tag] = struct{}{}
	}
	return float64(intersection) / float64(len(union))
}

func SharedTagCount(left, right []string) int {
	rightSet := stringSet(NormalizeTags(right))
	count := 0
	for _, tag := range NormalizeTags(left) {
		if _, ok := rightSet[tag]; ok {
			count++
		}
	}
	return count
}

func DurationSimilarity(leftSeconds, rightSeconds float64) float64 {
	if !positiveFinite(leftSeconds) || !positiveFinite(rightSeconds) {
		return 0
	}
	longer := math.Max(leftSeconds, rightSeconds)
	if longer <= 0 {
		return 0
	}
	return clamp01(1 - math.Min(math.Abs(leftSeconds-rightSeconds)/longer, 1))
}

func ResolutionSimilarity(left, right Scene) float64 {
	leftBucket, leftOK := resolutionBucket(left.Width, left.Height)
	rightBucket, rightOK := resolutionBucket(right.Width, right.Height)
	if !leftOK || !rightOK {
		return 0
	}
	if leftBucket == rightBucket {
		return 1
	}
	if math.Abs(float64(leftBucket-rightBucket)) == 1 {
		return 0.5
	}
	return 0
}

func HybridV3LiteScore(source, candidate Scene) (Result, bool) {
	if strings.TrimSpace(source.ID) == "" || source.ID == candidate.ID {
		return Result{}, false
	}
	weights := metadataRichWeights
	if len(NormalizeTags(source.Tags)) < 2 {
		weights = tagSparseWeights
	}
	distance, hasDistance := PHashDistance(source.PHash, candidate.PHash)
	breakdown := Breakdown{
		Tag:        TagSimilarity(source.Tags, candidate.Tags),
		Filename:   FilenameSimilarity(source.FileName, candidate.FileName),
		Visual:     0,
		PHash:      PHashSimilarity(source.PHash, candidate.PHash),
		Duration:   DurationSimilarity(source.DurationSeconds, candidate.DurationSeconds),
		Resolution: ResolutionSimilarity(source, candidate),
		Behavior:   0,
	}
	score := clamp01(weights.Tag*breakdown.Tag + weights.Filename*breakdown.Filename + weights.Visual*breakdown.Visual + weights.PHash*breakdown.PHash + weights.Duration*breakdown.Duration + weights.Resolution*breakdown.Resolution + weights.Behavior*breakdown.Behavior)
	if breakdown.Tag == 0 && breakdown.Filename == 0 && breakdown.PHash < 0.82 && breakdown.Visual < 0.9 {
		score = math.Min(score, 0.72)
	}
	if hasDistance && distance <= 8 {
		score = clamp01(score + 0.03)
	}
	return Result{SceneID: candidate.ID, Score: score, Reasons: reasons(source, candidate, breakdown, distance, hasDistance), Breakdown: breakdown, Weights: weights}, true
}

func HasLiteCandidateSignal(source, candidate Scene) bool {
	if source.ID == candidate.ID {
		return false
	}
	if SharedTagCount(source.Tags, candidate.Tags) > 0 {
		return true
	}
	if FilenameSimilarity(source.FileName, candidate.FileName) > 0 {
		return true
	}
	if distance, ok := PHashDistance(source.PHash, candidate.PHash); ok && distance <= 16 {
		return true
	}
	if DurationSimilarity(source.DurationSeconds, candidate.DurationSeconds) >= 0.85 {
		return true
	}
	return ResolutionSimilarity(source, candidate) >= 0.5
}

func reasons(source, candidate Scene, breakdown Breakdown, distance int, hasDistance bool) []string {
	var out []string
	if shared := SharedTagCount(source.Tags, candidate.Tags); shared > 0 {
		out = append(out, strconv.Itoa(shared)+" shared tags")
	}
	if breakdown.Filename >= 0.34 {
		out = append(out, "Similar filename")
	}
	if hasDistance && distance <= 8 {
		out = append(out, "Near-duplicate pHash")
	} else if hasDistance && distance <= 12 {
		out = append(out, "Close pHash")
	}
	if breakdown.Duration >= 0.85 {
		out = append(out, "Similar duration")
	}
	if breakdown.Resolution == 1 {
		out = append(out, "Same resolution")
	}
	if len(out) > 5 {
		return out[:5]
	}
	return out
}

func resolutionBucket(width, height int) (int, bool) {
	longEdge := max(width, height)
	shortEdge := minPositive(width, height)
	effectiveHeight := shortEdge
	if effectiveHeight <= 0 {
		effectiveHeight = longEdge
	}
	switch {
	case effectiveHeight >= 2160 || longEdge >= 3840:
		return 4, true
	case effectiveHeight >= 1440 || longEdge >= 2560:
		return 3, true
	case effectiveHeight >= 1080 || longEdge >= 1920:
		return 2, true
	case effectiveHeight >= 720 || longEdge >= 1280:
		return 1, true
	case effectiveHeight > 0 || longEdge > 0:
		return 0, true
	default:
		return 0, false
	}
}

func stringSet(values []string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		out[value] = struct{}{}
	}
	return out
}

func isDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func positiveFinite(value float64) bool {
	return value > 0 && !math.IsInf(value, 0) && !math.IsNaN(value)
}

func clamp01(value float64) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0
	}
	return math.Min(1, math.Max(0, value))
}

func minPositive(left, right int) int {
	if left <= 0 {
		return right
	}
	if right <= 0 {
		return left
	}
	if left < right {
		return left
	}
	return right
}
