package models

import "time"

// Pin represents a location pinned on the travel map.
type Pin struct {
	ID          int64     `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Latitude    float64   `json:"latitude"`
	Longitude   float64   `json:"longitude"`
	Color       string    `json:"color"`        // hex color e.g. "#FF5722"
	Icon        string    `json:"icon"`         // emoji or icon name
	VisitedAt   string    `json:"visited_at"`   // display date string
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// GoogleMapsImport represents a location from a Google Maps JSON export.
type GoogleMapsImport struct {
	Locations []GoogleMapsLocation `json:"features"`
}

type GoogleMapsLocation struct {
	Type     string                 `json:"type"`
	Geometry GoogleMapsGeometry     `json:"geometry"`
	Properties map[string]interface{} `json:"properties"`
}

type GoogleMapsGeometry struct {
	Type        string    `json:"type"`
	Coordinates []float64 `json:"coordinates"` // [lng, lat] in GeoJSON
}
