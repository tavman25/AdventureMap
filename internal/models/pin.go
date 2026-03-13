package models

import "time"

// Pin represents a location pinned on the travel map.
type Pin struct {
	ID          int64     `json:"id"`
	OwnerKey    string    `json:"-"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	ImageURL    string    `json:"image_url"`
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

// AuthEvent represents a recorded authentication attempt.
type AuthEvent struct {
	ID            int64     `json:"id"`
	Provider      string    `json:"provider"`
	Email         string    `json:"email"`
	Identity      string    `json:"identity"`
	IP            string    `json:"ip"`
	ForwardedFor  string    `json:"forwarded_for"`
	HostName      string    `json:"host_name"`
	UserAgent     string    `json:"user_agent"`
	Success       bool      `json:"success"`
	FailureReason string    `json:"failure_reason"`
	CreatedAt     time.Time `json:"created_at"`
}

// CloneInvite represents a one-time map clone invitation.
type CloneInvite struct {
	ID            int64     `json:"id"`
	OwnerKey      string    `json:"owner_key"`
	IncludePhotos bool      `json:"include_photos"`
	ExpiresAt     time.Time `json:"expires_at"`
	MaxUses       int       `json:"max_uses"`
	UsedCount     int       `json:"used_count"`
	Revoked       bool      `json:"revoked"`
	CreatedAt     time.Time `json:"created_at"`
}

// UserProfile stores per-owner UI settings.
type UserProfile struct {
	OwnerKey    string    `json:"owner_key"`
	DisplayTitle string   `json:"display_title"`
	UpdatedAt   time.Time `json:"updated_at"`
}
