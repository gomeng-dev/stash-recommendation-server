package graphql

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gomeng-dev/stash-web-sprite-similarity-lab/engine/go/internal/scoring"
)

const defaultPerPage = 500

const findScenesQuery = `query HybridEngineFindScenes($page: Int!, $perPage: Int!) {
  findScenes(filter: { page: $page, per_page: $perPage }) {
    count
    scenes {
      id
      title
      updated_at
      rating100
      play_count
      files {
        basename
        path
        duration
        width
        height
        fingerprint(type: "phash")
      }
      tags { name }
      paths { screenshot vtt sprite }
    }
  }
}`

const findSceneIDsQuery = `query HybridEngineFindSceneIDs($page: Int!, $perPage: Int!) {
  findScenes(filter: { page: $page, per_page: $perPage }) {
    count
    scenes { id }
  }
}`

type Client struct {
	URL         string
	APIKey      string
	CookieName  string
	CookieValue string
	HTTPClient  *http.Client
}

func (c Client) FetchAllScenes(ctx context.Context, maxScenes int, perPage int) ([]scoring.Scene, error) {
	if strings.TrimSpace(c.URL) == "" {
		return nil, fmt.Errorf("stash GraphQL URL is required")
	}
	if perPage <= 0 {
		perPage = defaultPerPage
	}
	if perPage > 1000 {
		perPage = 1000
	}
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	page := 1
	var out []scoring.Scene
	total := 0
	for {
		result, err := c.fetchPage(ctx, client, findScenesQuery, page, perPage)
		if err != nil {
			return nil, err
		}
		if total == 0 {
			total = result.Count
		}
		for _, scene := range result.Scenes {
			mapped := MapScene(scene)
			if mapped.ID == "" {
				continue
			}
			out = append(out, mapped)
			if maxScenes > 0 && len(out) >= maxScenes {
				return out, nil
			}
		}
		if len(out) >= total || len(result.Scenes) == 0 {
			return out, nil
		}
		page++
	}
}

func (c Client) FetchAllSceneIDs(ctx context.Context, maxScenes int, perPage int) ([]string, error) {
	if strings.TrimSpace(c.URL) == "" {
		return nil, fmt.Errorf("stash GraphQL URL is required")
	}
	if perPage <= 0 {
		perPage = defaultPerPage
	}
	if perPage > 1000 {
		perPage = 1000
	}
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	page := 1
	var out []string
	total := 0
	for {
		result, err := c.fetchPage(ctx, client, findSceneIDsQuery, page, perPage)
		if err != nil {
			return nil, err
		}
		if total == 0 {
			total = result.Count
		}
		for _, scene := range result.Scenes {
			id := clean(scene.ID)
			if id == "" {
				continue
			}
			out = append(out, id)
			if maxScenes > 0 && len(out) >= maxScenes {
				return out, nil
			}
		}
		if len(out) >= total || len(result.Scenes) == 0 {
			return out, nil
		}
		page++
	}
}

func (c Client) fetchPage(ctx context.Context, client *http.Client, query string, page, perPage int) (findScenesPayload, error) {
	body, err := json.Marshal(map[string]any{
		"query": query,
		"variables": map[string]any{
			"page":    page,
			"perPage": perPage,
		},
	})
	if err != nil {
		return findScenesPayload{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, bytes.NewReader(body))
	if err != nil {
		return findScenesPayload{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("ApiKey", c.APIKey)
	}
	if c.CookieName != "" && c.CookieValue != "" {
		req.AddCookie(&http.Cookie{Name: c.CookieName, Value: c.CookieValue})
	}
	res, err := client.Do(req)
	if err != nil {
		return findScenesPayload{}, fmt.Errorf("call Stash GraphQL: %w", err)
	}
	defer res.Body.Close()
	var decoded graphQLResponse
	if err := json.NewDecoder(res.Body).Decode(&decoded); err != nil {
		return findScenesPayload{}, fmt.Errorf("decode Stash GraphQL response: %w", err)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return findScenesPayload{}, fmt.Errorf("Stash GraphQL HTTP status %d", res.StatusCode)
	}
	if len(decoded.Errors) > 0 {
		return findScenesPayload{}, fmt.Errorf("Stash GraphQL error: %s", decoded.Errors[0].Message)
	}
	return decoded.Data.FindScenes, nil
}

type graphQLResponse struct {
	Data struct {
		FindScenes findScenesPayload `json:"findScenes"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

type findScenesPayload struct {
	Count  int          `json:"count"`
	Scenes []stashScene `json:"scenes"`
}

type stashScene struct {
	ID        string      `json:"id"`
	Title     string      `json:"title"`
	UpdatedAt string      `json:"updated_at"`
	Rating100 int         `json:"rating100"`
	PlayCount int         `json:"play_count"`
	Files     []stashFile `json:"files"`
	Tags      []stashTag  `json:"tags"`
	Paths     stashPaths  `json:"paths"`
}

type stashFile struct {
	Basename    string  `json:"basename"`
	Path        string  `json:"path"`
	Duration    float64 `json:"duration"`
	Width       int     `json:"width"`
	Height      int     `json:"height"`
	Fingerprint string  `json:"fingerprint"`
}

type stashTag struct {
	Name string `json:"name"`
}

type stashPaths struct {
	Screenshot string `json:"screenshot"`
	VTT        string `json:"vtt"`
	Sprite     string `json:"sprite"`
}

func MapScene(scene stashScene) scoring.Scene {
	var file stashFile
	if len(scene.Files) > 0 {
		file = scene.Files[0]
	}
	title := clean(scene.Title)
	fileName := clean(file.Basename)
	if title == "" {
		title = fileName
	}
	if title == "" {
		title = clean(scene.ID)
	}
	if fileName == "" {
		fileName = title
	}
	tags := make([]string, 0, len(scene.Tags))
	for _, tag := range scene.Tags {
		if name := clean(tag.Name); name != "" {
			tags = append(tags, name)
		}
	}
	return scoring.Scene{
		ID:              clean(scene.ID),
		Title:           title,
		FileName:        fileName,
		DurationSeconds: file.Duration,
		Width:           file.Width,
		Height:          file.Height,
		Tags:            tags,
		ThumbnailURL:    clean(scene.Paths.Screenshot),
		SpriteImageURL:  clean(scene.Paths.Sprite),
		SpriteVTTURL:    clean(scene.Paths.VTT),
		PHash:           scoring.NormalizePHash(file.Fingerprint),
		Rating100:       scene.Rating100,
		PlayCount:       scene.PlayCount,
		StashUpdatedAt:  clean(scene.UpdatedAt),
	}
}

func clean(value string) string {
	return strings.TrimSpace(value)
}
