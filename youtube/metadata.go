// Package youtube ดึง metadata ของ Video จาก YouTube โดยไม่ใช้ Data API
package youtube

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var httpClient = &http.Client{Timeout: 5 * time.Second}

// VideoMetadata คือข้อมูล Video ที่ดึงมาได้
type VideoMetadata struct {
	Title     string
	Thumbnail string
	// LikelyBroadcastLive จากชื่อใน oEmbed (เช่น "🔴LIVE : …") — watch?v= ไม่เข้าข่าย URL /live/ ได้
	LikelyBroadcastLive bool
}

// oEmbedResponse คือ Response จาก YouTube oEmbed API
type oEmbedResponse struct {
	Title        string `json:"title"`
	ThumbnailURL string `json:"thumbnail_url"`
}

// FetchMetadata ดึง Title และ Thumbnail จาก YouTube Video ID
// - ใช้ oEmbed API สำหรับ Title
// - ลอง maxresdefault.jpg ก่อน ถ้า 404 fallback เป็น hqdefault.jpg
func FetchMetadata(videoID string) (*VideoMetadata, error) {
	title, err := fetchTitle(videoID)
	if err != nil {
		return nil, err
	}
	thumbnail := resolveThumbnail(videoID)
	return &VideoMetadata{
		Title:               title,
		Thumbnail:           thumbnail,
		LikelyBroadcastLive: LikelyBroadcastLiveFromOEmbedTitle(title),
	}, nil
}

// LikelyBroadcastLiveFromOEmbedTitle ใช้ title จาก oEmbed ช่วยเดาทีวีถ่ายทอดสด (ไม่เรียก Data API)
func LikelyBroadcastLiveFromOEmbedTitle(title string) bool {
	title = strings.TrimSpace(title)
	if title == "" {
		return false
	}
	up := strings.ToUpper(title)
	// ผู้เล่นหลายช่องในไทยใส่ "🔴" + "LIVE" ใน title
	if strings.Contains(title, "🔴") && strings.Contains(up, "LIVE") {
		return true
	}
	if strings.Contains(up, " LIVE:") || strings.Contains(up, " LIVE :") {
		return true
	}
	if strings.Contains(title, "ถ่ายทอดสด") {
		return true
	}
	return false
}

func fetchTitle(videoID string) (string, error) {
	videoURL := "https://www.youtube.com/watch?v=" + url.QueryEscape(videoID)
	resp, err := httpClient.Get("https://www.youtube.com/oembed?url=" + url.QueryEscape(videoURL) + "&format=json")
	if err != nil {
		return "", fmt.Errorf("fetchTitle: http get: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetchTitle: oembed returned %d", resp.StatusCode)
	}

	var result oEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("fetchTitle: decode: %w", err)
	}
	return result.Title, nil
}

// resolveThumbnail ลอง maxresdefault ก่อน ถ้า 404 ใช้ hqdefault
func resolveThumbnail(videoID string) string {
	maxres := fmt.Sprintf("https://i.ytimg.com/vi/%s/maxresdefault.jpg", videoID)
	resp, err := httpClient.Head(maxres)
	if err == nil && resp.StatusCode == http.StatusOK {
		return maxres
	}
	return fmt.Sprintf("https://i.ytimg.com/vi/%s/hqdefault.jpg", videoID)
}
