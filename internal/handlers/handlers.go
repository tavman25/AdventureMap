package handlers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"travel-map/internal/database"
	"travel-map/internal/models"

	"github.com/gin-gonic/gin"
)

// Handler holds shared dependencies for all HTTP handlers.
type Handler struct {
	DB *database.DB
}

// New creates a new Handler.
func New(db *database.DB) *Handler {
	return &Handler{DB: db}
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
