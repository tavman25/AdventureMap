package handlers

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"travel-map/internal/database"
	"travel-map/internal/models"

	"github.com/gin-gonic/gin"
)

// Handler holds shared dependencies for all HTTP handlers.
type Handler struct {
	DB            *database.DB
	UploadDir     string
	AdminPassword string
	AuthSecret    string
}

// New creates a new Handler.
func New(db *database.DB, uploadDir, adminPassword, authSecret string) *Handler {
	return &Handler{DB: db, UploadDir: uploadDir, AdminPassword: adminPassword, AuthSecret: authSecret}
}

func (h *Handler) authEnabled() bool {
	return strings.TrimSpace(h.AdminPassword) != ""
}

func (h *Handler) adminCookieName() string {
	return "travel_map_admin"
}

func (h *Handler) authCookieValue() string {
	mac := hmac.New(sha256.New, []byte(h.AuthSecret))
	mac.Write([]byte(h.AdminPassword))
	return fmt.Sprintf("%x", mac.Sum(nil))
}

func (h *Handler) hasAdminCookie(c *gin.Context) bool {
	if !h.authEnabled() {
		return true
	}
	cookie, err := c.Cookie(h.adminCookieName())
	if err != nil {
		return false
	}
	expected := h.authCookieValue()
	if len(cookie) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(cookie), []byte(expected)) == 1
}

func (h *Handler) RequireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !h.hasAdminCookie(c) {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "admin login required"})
			c.Abort()
			return
		}
		c.Next()
	}
}

func (h *Handler) GetAuthStatus(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"auth_enabled": h.authEnabled(),
		"is_admin":     h.hasAdminCookie(c),
	})
}

func (h *Handler) Login(c *gin.Context) {
	if !h.authEnabled() {
		c.JSON(http.StatusOK, gin.H{"message": "auth disabled", "is_admin": true})
		return
	}

	var payload struct {
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid login payload"})
		return
	}
	if subtle.ConstantTimeCompare([]byte(payload.Password), []byte(h.AdminPassword)) != 1 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid password"})
		return
	}

	secureCookie := c.Request.TLS != nil
	c.SetCookie(h.adminCookieName(), h.authCookieValue(), 60*60*24*14, "/", "", secureCookie, true)
	c.JSON(http.StatusOK, gin.H{"message": "logged in", "is_admin": true})
}

func (h *Handler) Logout(c *gin.Context) {
	secureCookie := c.Request.TLS != nil
	c.SetCookie(h.adminCookieName(), "", -1, "/", "", secureCookie, true)
	c.JSON(http.StatusOK, gin.H{"message": "logged out"})
}

// GetPins returns all pins as JSON.
func (h *Handler) GetPins(c *gin.Context) {
	pins, err := h.DB.GetAllPins()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, pins)
}

// GetPin returns a single pin by id.
func (h *Handler) GetPin(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	pin, err := h.DB.GetPin(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "pin not found"})
		return
	}
	c.JSON(http.StatusOK, pin)
}

// CreatePin creates a new pin.
func (h *Handler) CreatePin(c *gin.Context) {
	var pin models.Pin
	if err := c.ShouldBindJSON(&pin); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if pin.Title == "" {
		pin.Title = "Visited Place"
	}
	if pin.Color == "" {
		pin.Color = "#FF5722"
	}
	if pin.Icon == "" {
		pin.Icon = "📍"
	}
	if err := h.DB.CreatePin(&pin); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, pin)
}

// UpdatePin modifies an existing pin.
func (h *Handler) UpdatePin(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	existing, err := h.DB.GetPin(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "pin not found"})
		return
	}
	if err := c.ShouldBindJSON(existing); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	existing.ID = id
	if err := h.DB.UpdatePin(existing); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, existing)
}

// DeletePin removes a pin by id.
func (h *Handler) DeletePin(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	if err := h.DB.DeletePin(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "pin deleted"})
}

// ImportGoogleMaps ingests a Google Maps Takeout JSON (GeoJSON FeatureCollection).
func (h *Handler) ImportGoogleMaps(c *gin.Context) {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 10<<20)) // 10MB limit
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read body"})
		return
	}

	// Try GeoJSON FeatureCollection first (Google Takeout saved-places.json format)
	var geoJSON struct {
		Type     string `json:"type"`
		Features []struct {
			Type     string `json:"type"`
			Geometry struct {
				Type        string    `json:"type"`
				Coordinates []float64 `json:"coordinates"`
			} `json:"geometry"`
			Properties map[string]interface{} `json:"properties"`
		} `json:"features"`
	}

	pins := []models.Pin{}

	if err := json.Unmarshal(body, &geoJSON); err == nil && geoJSON.Type == "FeatureCollection" {
		for _, f := range geoJSON.Features {
			if f.Geometry.Type != "Point" || len(f.Geometry.Coordinates) < 2 {
				continue
			}
			p := models.Pin{
				Latitude:  f.Geometry.Coordinates[1],
				Longitude: f.Geometry.Coordinates[0],
				Color:     "#4CAF50",
				Icon:      "⭐",
			}
			if name, ok := f.Properties["name"].(string); ok {
				p.Title = name
			} else {
				p.Title = "Imported Place"
			}
			if addr, ok := f.Properties["address"].(string); ok {
				p.Description = addr
			}
			if date, ok := f.Properties["Published"].(string); ok {
				p.VisitedAt = date
			}
			p.ImageURL = extractImageURL(f.Properties)
			pins = append(pins, p)
		}
	} else {
		// Try flat array format from some Google exports
		var flat []map[string]interface{}
		if err := json.Unmarshal(body, &flat); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported JSON format"})
			return
		}
		for _, item := range flat {
			lat, lon := extractLatLon(item)
			if lat == 0 && lon == 0 {
				continue
			}
			p := models.Pin{
				Latitude:  lat,
				Longitude: lon,
				Color:     "#4CAF50",
				Icon:      "⭐",
			}
			if name, ok := item["name"].(string); ok {
				p.Title = name
			} else {
				p.Title = "Imported Place"
			}
			if addr, ok := item["address"].(string); ok {
				p.Description = addr
			}
			p.ImageURL = extractImageURL(item)
			pins = append(pins, p)
		}
	}

	if len(pins) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no valid locations found in JSON"})
		return
	}

	count, err := h.DB.BulkCreatePins(pins)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"message":  fmt.Sprintf("Successfully imported %d pins", count),
		"imported": count,
	})
}

// ImportPinPhotos ingests photo URLs and applies them to existing pins.
// Supported JSON format (array):
// [{"id":1,"image_url":"https://..."}, {"title":"Rome","image_url":"https://..."}]
func (h *Handler) ImportPinPhotos(c *gin.Context) {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 10<<20)) // 10MB limit
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read body"})
		return
	}

	var rows []map[string]interface{}
	if err := json.Unmarshal(body, &rows); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid JSON: expected an array of objects"})
		return
	}
	if len(rows) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "empty import payload"})
		return
	}

	updated := 0
	skipped := 0
	for _, row := range rows {
		imageURL := strings.TrimSpace(extractImageURL(row))
		if imageURL == "" {
			skipped++
			continue
		}

		if idVal, ok := row["id"]; ok {
			if id, ok := toInt64(idVal); ok && id > 0 {
				okUpdated, err := h.DB.UpdatePinImageByID(id, imageURL)
				if err != nil {
					c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
					return
				}
				if okUpdated {
					updated++
				} else {
					skipped++
				}
				continue
			}
		}

		title := strings.TrimSpace(toString(row["title"]))
		if title == "" {
			skipped++
			continue
		}
		affected, err := h.DB.UpdatePinImageByTitle(title, imageURL)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		if affected > 0 {
			updated += int(affected)
		} else {
			skipped++
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"message": fmt.Sprintf("Imported photos for %d pin(s)", updated),
		"updated": updated,
		"skipped": skipped,
	})
}

// UploadImage stores an uploaded image file and returns a URL that can be used in pins.
func (h *Handler) UploadImage(c *gin.Context) {
	file, err := c.FormFile("image")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing image file"})
		return
	}

	ext := strings.ToLower(filepath.Ext(file.Filename))
	allowed := map[string]bool{
		".jpg":  true,
		".jpeg": true,
		".png":  true,
		".webp": true,
		".gif":  true,
	}
	if !allowed[ext] {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported file type; use jpg, png, webp, or gif"})
		return
	}

	if err := os.MkdirAll(h.UploadDir, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to prepare upload directory"})
		return
	}

	randPart := make([]byte, 8)
	if _, err := rand.Read(randPart); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate file name"})
		return
	}
	fileName := fmt.Sprintf("%d-%x%s", time.Now().Unix(), randPart, ext)
	savePath := filepath.Join(h.UploadDir, fileName)

	if err := c.SaveUploadedFile(file, savePath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save image"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "image uploaded",
		"url":     "/uploads/" + fileName,
	})
}

func extractLatLon(item map[string]interface{}) (float64, float64) {
	// Try direct lat/lng fields
	if lat, ok := item["latitude"].(float64); ok {
		if lon, ok := item["longitude"].(float64); ok {
			return lat, lon
		}
	}
	// Try nested location object
	if loc, ok := item["location"].(map[string]interface{}); ok {
		lat, _ := loc["latitude"].(float64)
		lon, _ := loc["longitude"].(float64)
		return lat, lon
	}
	// Try latE7/lngE7 (Google Location History format)
	if latE7, ok := item["latitudeE7"].(float64); ok {
		if lonE7, ok := item["longitudeE7"].(float64); ok {
			return latE7 / 1e7, lonE7 / 1e7
		}
	}
	return 0, 0
}

func extractImageURL(item map[string]interface{}) string {
	keys := []string{"image_url", "photo_url", "image", "photo", "url", "thumbnail"}
	for _, k := range keys {
		if val, ok := item[k]; ok {
			s := strings.TrimSpace(toString(val))
			if strings.HasPrefix(strings.ToLower(s), "http://") || strings.HasPrefix(strings.ToLower(s), "https://") {
				return s
			}
		}
	}
	return ""
}

func toString(v interface{}) string {
	s, _ := v.(string)
	return s
}

func toInt64(v interface{}) (int64, bool) {
	switch x := v.(type) {
	case float64:
		return int64(x), true
	case int64:
		return x, true
	case int:
		return int64(x), true
	case string:
		id, err := strconv.ParseInt(strings.TrimSpace(x), 10, 64)
		if err != nil {
			return 0, false
		}
		return id, true
	default:
		return 0, false
	}
}
