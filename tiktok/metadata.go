// Package tiktok ดึง metadata ของ TikTok video และ resolve short URLs
package tiktok

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var httpClient = &http.Client{
	Timeout: 10 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return fmt.Errorf("too many redirects")
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
		return nil
	},
}

// videoIDRegex ตรงกับ TikTok video ID ซึ่งเป็นตัวเลข 1–25 หลัก
var videoIDRegex = regexp.MustCompile(`^\d{1,25}$`)

// VideoMetadata คือข้อมูล Video ที่ดึงมาได้
type VideoMetadata struct {
	Title     string
	Thumbnail string
}

type oEmbedResponse struct {
	Title        string `json:"title"`
	ThumbnailURL string `json:"thumbnail_url"`
}

// IsShortHost รายงานว่า host เป็น short URL host ของ TikTok
func IsShortHost(host string) bool {
	return host == "vt.tiktok.com" || host == "vm.tiktok.com"
}

// IsValidURL รายงานว่า URL เป็น TikTok URL ที่รองรับ
func IsValidURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	switch u.Host {
	case "vt.tiktok.com", "vm.tiktok.com", "www.tiktok.com", "tiktok.com", "m.tiktok.com":
		return true
	}
	return false
}

// resolveShortURL follows redirects และคืน final URL
func resolveShortURL(rawURL string) (string, error) {
	req, err := http.NewRequest("GET", rawURL, nil)
	if err != nil {
		return "", fmt.Errorf("resolveShortURL: build request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("resolveShortURL: %w", err)
	}
	resp.Body.Close()
	return resp.Request.URL.String(), nil
}

// ExtractVideoID ดึง TikTok video ID จาก URL
// รองรับ: www.tiktok.com/@user/video/ID และ short URLs (vt.tiktok.com, vm.tiktok.com)
func ExtractVideoID(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse url: %w", err)
	}

	if IsShortHost(u.Host) {
		resolved, err := resolveShortURL(rawURL)
		if err != nil {
			return "", fmt.Errorf("resolve short url: %w", err)
		}
		return ExtractVideoID(resolved)
	}

	switch u.Host {
	case "www.tiktok.com", "tiktok.com", "m.tiktok.com":
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		for i, p := range parts {
			if p == "video" && i+1 < len(parts) {
				id := parts[i+1]
				if videoIDRegex.MatchString(id) {
					return id, nil
				}
			}
		}
		// path ไม่มี /video/ (เช่น /t/ZT8xxx) — ลอง resolve redirect
		resolved, err := resolveShortURL(rawURL)
		if err != nil {
			return "", fmt.Errorf("no video id found in path: %s", u.Path)
		}
		if resolved == rawURL {
			return "", fmt.Errorf("no video id found in path: %s", u.Path)
		}
		return ExtractVideoID(resolved)
	default:
		return "", fmt.Errorf("unsupported tiktok host: %s", u.Host)
	}
}

// FetchMetadata ดึง title และ thumbnail จาก TikTok video ID ผ่าน oEmbed API
func FetchMetadata(videoID string) (*VideoMetadata, error) {
	videoURL := "https://www.tiktok.com/video/" + videoID
	apiURL := "https://www.tiktok.com/oembed?url=" + url.QueryEscape(videoURL)
	resp, err := httpClient.Get(apiURL)
	if err != nil {
		return nil, fmt.Errorf("FetchMetadata: http get: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("FetchMetadata: oembed returned %d", resp.StatusCode)
	}
	var result oEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("FetchMetadata: decode: %w", err)
	}
	return &VideoMetadata{
		Title:     result.Title,
		Thumbnail: result.ThumbnailURL,
	}, nil
}
