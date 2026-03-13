package handlers

import (
	"context"
	"database/sql"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
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
	GoogleClientID string
	AllowedEmails  map[string]struct{}
}

// New creates a new Handler.
func New(db *database.DB, uploadDir, adminPassword, authSecret, googleClientID, allowedEmails string) *Handler {
	return &Handler{
		DB:             db,
		UploadDir:      uploadDir,
		AdminPassword:  adminPassword,
		AuthSecret:     authSecret,
		GoogleClientID: strings.TrimSpace(googleClientID),
		AllowedEmails:  parseAllowedEmails(allowedEmails),
	}
}

func (h *Handler) authEnabled() bool {
	return h.passwordAuthEnabled() || h.googleAuthEnabled()
}

func (h *Handler) passwordAuthEnabled() bool {
	return strings.TrimSpace(h.AdminPassword) != ""
}

func (h *Handler) googleAuthEnabled() bool {
	return strings.TrimSpace(h.GoogleClientID) != ""
}

func (h *Handler) adminCookieName() string {
	return "travel_map_admin"
}

func (h *Handler) signValue(value string) string {
	mac := hmac.New(sha256.New, []byte(h.AuthSecret))
	mac.Write([]byte(value))
	return fmt.Sprintf("%x", mac.Sum(nil))
}

func (h *Handler) buildSessionValue(identity string) string {
	expiresAt := time.Now().UTC().Add(14 * 24 * time.Hour).Unix()
	payload := fmt.Sprintf("v1|%s|%d", identity, expiresAt)
	return fmt.Sprintf("%s|%s", payload, h.signValue(payload))
}

func (h *Handler) parseSessionValue(cookie string) (string, bool) {
	parts := strings.Split(cookie, "|")
	if len(parts) != 4 || parts[0] != "v1" {
		return "", false
	}
	payload := strings.Join(parts[:3], "|")
	expectedSig := h.signValue(payload)
	if len(parts[3]) != len(expectedSig) || subtle.ConstantTimeCompare([]byte(parts[3]), []byte(expectedSig)) != 1 {
		return "", false
	}
	expiresAt, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || time.Now().UTC().Unix() > expiresAt {
		return "", false
	}
	return parts[1], true
}

func (h *Handler) isSecureRequest(c *gin.Context) bool {
	if c.Request.TLS != nil {
		return true
	}
	return strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https")
}

func (h *Handler) setSessionCookie(c *gin.Context, identity string) {
	c.SetCookie(h.adminCookieName(), h.buildSessionValue(identity), 60*60*24*14, "/", "", h.isSecureRequest(c), true)
}

func (h *Handler) hasAdminCookie(c *gin.Context) bool {
	if !h.authEnabled() {
		return true
	}
	_, ok := h.currentIdentity(c)
	return ok
}

func (h *Handler) currentIdentity(c *gin.Context) (string, bool) {
	if !h.authEnabled() {
		return "", true
	}
	cookie, err := c.Cookie(h.adminCookieName())
	if err != nil {
		return "", false
	}
	identity, ok := h.parseSessionValue(cookie)
	return identity, ok
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

func (h *Handler) RequireSuperAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := h.currentIdentity(c)
		if !ok || identity != "password-admin" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "super-admin login required"})
			c.Abort()
			return
		}
		c.Next()
	}
}

func (h *Handler) resolveOwnerKey(c *gin.Context) (string, bool) {
	identity, ok := h.currentIdentity(c)
	if !ok {
		return "", false
	}
	if identity == "" {
		return "", true
	}
	return identity, true
}

func (h *Handler) requireOwnerKey(c *gin.Context) (string, bool) {
	if !h.authEnabled() {
		return "password-admin", true
	}
	identity, ok := h.currentIdentity(c)
	if !ok || strings.TrimSpace(identity) == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "login required"})
		return "", false
	}
	return identity, true
}

func (h *Handler) GetAuthStatus(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"auth_enabled":           h.authEnabled(),
		"is_admin":               h.hasAdminCookie(c),
		"password_login_enabled": h.passwordAuthEnabled(),
		"google_login_enabled":   h.googleAuthEnabled(),
		"google_client_id":       h.GoogleClientID,
	})
}

func (h *Handler) Login(c *gin.Context) {
	if !h.passwordAuthEnabled() {
		c.JSON(http.StatusOK, gin.H{"message": "auth disabled", "is_admin": true})
		return
	}

	var payload struct {
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		h.logAuthAttempt(c, models.AuthEvent{Provider: "password", Success: false, FailureReason: "invalid payload"})
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid login payload"})
		return
	}
	if subtle.ConstantTimeCompare([]byte(payload.Password), []byte(h.AdminPassword)) != 1 {
		h.logAuthAttempt(c, models.AuthEvent{Provider: "password", Success: false, FailureReason: "invalid password"})
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid password"})
		return
	}

	h.setSessionCookie(c, "password-admin")
	h.logAuthAttempt(c, models.AuthEvent{Provider: "password", Identity: "password-admin", Success: true})
	c.JSON(http.StatusOK, gin.H{"message": "logged in", "is_admin": true})
}

func (h *Handler) GoogleLogin(c *gin.Context) {
	if !h.googleAuthEnabled() {
		h.logAuthAttempt(c, models.AuthEvent{Provider: "google", Success: false, FailureReason: "google login disabled"})
		c.JSON(http.StatusBadRequest, gin.H{"error": "google login is not configured"})
		return
	}

	var payload struct {
		Credential string `json:"credential"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil || strings.TrimSpace(payload.Credential) == "" {
		h.logAuthAttempt(c, models.AuthEvent{Provider: "google", Success: false, FailureReason: "invalid google payload"})
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid google login payload"})
		return
	}

	email, err := h.verifyGoogleCredential(c.Request.Context(), payload.Credential)
	if err != nil {
		h.logAuthAttempt(c, models.AuthEvent{Provider: "google", Success: false, FailureReason: err.Error()})
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}
	if !h.isAllowedEmail(email) {
		h.logAuthAttempt(c, models.AuthEvent{Provider: "google", Email: email, Identity: email, Success: false, FailureReason: "email not allowed"})
		c.JSON(http.StatusForbidden, gin.H{"error": "this Google account is not allowed to edit"})
		return
	}

	h.setSessionCookie(c, email)
	h.logAuthAttempt(c, models.AuthEvent{Provider: "google", Email: email, Identity: email, Success: true})
	c.JSON(http.StatusOK, gin.H{"message": "logged in", "is_admin": true, "email": email})
}

func (h *Handler) Logout(c *gin.Context) {
	c.SetCookie(h.adminCookieName(), "", -1, "/", "", h.isSecureRequest(c), true)
	c.JSON(http.StatusOK, gin.H{"message": "logged out"})
}

func (h *Handler) GetIPCheck(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	events, err := h.DB.ListAuthEvents(limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"events": events})
}

func (h *Handler) GetProfile(c *gin.Context) {
	ownerKey, ok := h.requireOwnerKey(c)
	if !ok {
		return
	}
	profile, err := h.DB.GetUserProfile(ownerKey)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"owner_key":     profile.OwnerKey,
		"display_title": profile.DisplayTitle,
		"updated_at":    profile.UpdatedAt,
	})
}

func (h *Handler) UpdateProfile(c *gin.Context) {
	ownerKey, ok := h.requireOwnerKey(c)
	if !ok {
		return
	}
	var payload struct {
		DisplayTitle string `json:"display_title"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid profile payload"})
		return
	}
	title := strings.TrimSpace(payload.DisplayTitle)
	if len(title) > 80 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "display title is too long"})
		return
	}
	if err := h.DB.UpsertUserProfileDisplayTitle(ownerKey, title); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "profile updated", "display_title": title})
}

func (h *Handler) CreateCloneInvite(c *gin.Context) {
	ownerKey, ok := h.requireOwnerKey(c)
	if !ok {
		return
	}

	var payload struct {
		IncludePhotos bool `json:"include_photos"`
		ExpiresHours  int  `json:"expires_hours"`
		MaxUses       int  `json:"max_uses"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid clone invite payload"})
		return
	}
	if payload.ExpiresHours <= 0 {
		payload.ExpiresHours = 72
	}
	if payload.ExpiresHours > (24 * 30) {
		payload.ExpiresHours = 24 * 30
	}
	if payload.MaxUses <= 0 {
		payload.MaxUses = 1
	}
	if payload.MaxUses > 10 {
		payload.MaxUses = 10
	}

	inviteToken, tokenHash, err := generateInviteTokenAndHash()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create invite token"})
		return
	}

	expiresAt := time.Now().UTC().Add(time.Duration(payload.ExpiresHours) * time.Hour)
	invite, err := h.DB.CreateCloneInvite(ownerKey, tokenHash, payload.IncludePhotos, expiresAt, payload.MaxUses)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"invite_token":   inviteToken,
		"invite_id":      invite.ID,
		"include_photos": invite.IncludePhotos,
		"expires_at":     invite.ExpiresAt,
		"max_uses":       invite.MaxUses,
		"used_count":     invite.UsedCount,
	})
}

func (h *Handler) ListCloneInvites(c *gin.Context) {
	ownerKey, ok := h.requireOwnerKey(c)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	invites, err := h.DB.ListCloneInvitesByOwner(ownerKey, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"invites": invites})
}

func (h *Handler) RevokeCloneInvite(c *gin.Context) {
	ownerKey, ok := h.requireOwnerKey(c)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid invite id"})
		return
	}
	if err := h.DB.RevokeCloneInvite(ownerKey, id); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "invite not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "invite revoked"})
}

func (h *Handler) GetCloneEvents(c *gin.Context) {
	ownerKey, ok := h.requireOwnerKey(c)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	events, err := h.DB.ListCloneEventsBySourceOwner(ownerKey, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"events": events})
}

func (h *Handler) AcceptCloneInvite(c *gin.Context) {
	targetOwnerKey, ok := h.requireOwnerKey(c)
	if !ok {
		return
	}

	var payload struct {
		InviteToken string `json:"invite_token"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid clone accept payload"})
		return
	}
	token := strings.TrimSpace(payload.InviteToken)
	if token == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invite token is required"})
		return
	}

	invite, err := h.DB.GetCloneInviteByTokenHash(hashInviteToken(token))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "invite not found"})
		return
	}
	if invite.Revoked {
		c.JSON(http.StatusForbidden, gin.H{"error": "invite was revoked"})
		return
	}
	if invite.UsedCount >= invite.MaxUses {
		c.JSON(http.StatusForbidden, gin.H{"error": "invite has already been used"})
		return
	}
	if time.Now().UTC().After(invite.ExpiresAt) {
		c.JSON(http.StatusForbidden, gin.H{"error": "invite has expired"})
		return
	}
	if invite.OwnerKey == targetOwnerKey {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot clone your own map with this invite"})
		return
	}

	if err := h.DB.ConsumeCloneInvite(invite.ID); err != nil {
		c.JSON(http.StatusForbidden, gin.H{"error": "invite is no longer valid"})
		return
	}

	clonedCount, err := h.DB.ClonePinsToOwner(invite.OwnerKey, targetOwnerKey, invite.IncludePhotos)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	_ = h.DB.LogCloneEvent(invite.OwnerKey, targetOwnerKey, invite.ID, clonedCount, invite.IncludePhotos)

	c.JSON(http.StatusOK, gin.H{
		"message":         fmt.Sprintf("Cloned %d pin(s)", clonedCount),
		"cloned_count":    clonedCount,
		"include_photos":  invite.IncludePhotos,
		"source_owner_key": invite.OwnerKey,
	})
}

func (h *Handler) verifyGoogleCredential(ctx context.Context, credential string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://oauth2.googleapis.com/tokeninfo?id_token="+credential, nil)
	if err != nil {
		return "", fmt.Errorf("failed to build google verification request")
	}

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("google verification failed")
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("google sign-in was rejected")
	}

	var tokenInfo struct {
		Audience      string `json:"aud"`
		Email         string `json:"email"`
		EmailVerified string `json:"email_verified"`
		ExpiresAt     string `json:"exp"`
	}
	if err := json.NewDecoder(res.Body).Decode(&tokenInfo); err != nil {
		return "", fmt.Errorf("invalid google verification response")
	}
	if tokenInfo.Audience != h.GoogleClientID {
		return "", fmt.Errorf("google client mismatch")
	}
	if !strings.EqualFold(tokenInfo.EmailVerified, "true") {
		return "", fmt.Errorf("google email is not verified")
	}
	if strings.TrimSpace(tokenInfo.Email) == "" {
		return "", fmt.Errorf("google account email missing")
	}
	if tokenInfo.ExpiresAt != "" {
		exp, err := strconv.ParseInt(tokenInfo.ExpiresAt, 10, 64)
		if err != nil || time.Now().UTC().Unix() >= exp {
			return "", fmt.Errorf("google token expired")
		}
	}
	return strings.ToLower(strings.TrimSpace(tokenInfo.Email)), nil
}

func (h *Handler) isAllowedEmail(email string) bool {
	if len(h.AllowedEmails) == 0 {
		return true
	}
	_, ok := h.AllowedEmails[strings.ToLower(strings.TrimSpace(email))]
	return ok
}

func parseAllowedEmails(raw string) map[string]struct{} {
	allowed := map[string]struct{}{}
	for _, item := range strings.Split(raw, ",") {
		email := strings.ToLower(strings.TrimSpace(item))
		if email == "" {
			continue
		}
		allowed[email] = struct{}{}
	}
	return allowed
}

// GetPins returns all pins as JSON.
func (h *Handler) GetPins(c *gin.Context) {
	ownerKey, ok := h.resolveOwnerKey(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "login required"})
		return
	}
	pins, err := h.DB.GetAllPins(ownerKey)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, pins)
}

// GetPin returns a single pin by id.
func (h *Handler) GetPin(c *gin.Context) {
	ownerKey, ok := h.resolveOwnerKey(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "login required"})
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	pin, err := h.DB.GetPin(id, ownerKey)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "pin not found"})
		return
	}
	c.JSON(http.StatusOK, pin)
}

// ListRoutes returns all route connections for the current owner.
func (h *Handler) ListRoutes(c *gin.Context) {
	ownerKey, ok := h.resolveOwnerKey(c)
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "login required"})
		return
	}
	routes, err := h.DB.ListRouteConnections(ownerKey)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, routes)
}

// CreateRoute creates a route connection between two pins.
func (h *Handler) CreateRoute(c *gin.Context) {
	ownerKey, ok := h.requireOwnerKey(c)
	if !ok {
		return
	}
	var payload struct {
		FromPinID int64 `json:"from_pin_id"`
		ToPinID   int64 `json:"to_pin_id"`
	}
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid route payload"})
		return
	}
	if payload.FromPinID <= 0 || payload.ToPinID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "from_pin_id and to_pin_id are required"})
		return
	}
	if payload.FromPinID == payload.ToPinID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot connect a pin to itself"})
		return
	}

	route, err := h.DB.CreateRouteConnection(ownerKey, payload.FromPinID, payload.ToPinID)
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "one or both pins were not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, route)
}

// DeleteRoute removes a route connection.
func (h *Handler) DeleteRoute(c *gin.Context) {
	ownerKey, ok := h.requireOwnerKey(c)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid route id"})
		return
	}
	if err := h.DB.DeleteRouteConnection(ownerKey, id); err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "route not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "route deleted"})
}

// ClearRoutes removes all route connections for the current owner.
func (h *Handler) ClearRoutes(c *gin.Context) {
	ownerKey, ok := h.requireOwnerKey(c)
	if !ok {
		return
	}
	if err := h.DB.ClearRouteConnections(ownerKey); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "routes cleared"})
}

// CreatePin creates a new pin.
func (h *Handler) CreatePin(c *gin.Context) {
	ownerKey, ok := h.requireOwnerKey(c)
	if !ok {
		return
	}
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
	if err := h.DB.CreatePin(ownerKey, &pin); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, pin)
}

// UpdatePin modifies an existing pin.
func (h *Handler) UpdatePin(c *gin.Context) {
	ownerKey, ok := h.requireOwnerKey(c)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	existing, err := h.DB.GetPin(id, ownerKey)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "pin not found"})
		return
	}
	if err := c.ShouldBindJSON(existing); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	existing.ID = id
	if err := h.DB.UpdatePin(ownerKey, existing); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, existing)
}

// DeletePin removes a pin by id.
func (h *Handler) DeletePin(c *gin.Context) {
	ownerKey, ok := h.requireOwnerKey(c)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid id"})
		return
	}
	if err := h.DB.DeletePin(id, ownerKey); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "pin deleted"})
}

// ImportGoogleMaps ingests a Google Maps Takeout JSON (GeoJSON FeatureCollection).
func (h *Handler) ImportGoogleMaps(c *gin.Context) {
	ownerKey, ok := h.requireOwnerKey(c)
	if !ok {
		return
	}
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

	count, err := h.DB.BulkCreatePins(ownerKey, pins)
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
	ownerKey, ok := h.requireOwnerKey(c)
	if !ok {
		return
	}
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
				okUpdated, err := h.DB.UpdatePinImageByID(ownerKey, id, imageURL)
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
		affected, err := h.DB.UpdatePinImageByTitle(ownerKey, title, imageURL)
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
	ownerKey, ok := h.requireOwnerKey(c)
	if !ok {
		return
	}
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

	ownerDir := filepath.Join(h.UploadDir, sanitizePathSegment(ownerKey))
	if err := os.MkdirAll(ownerDir, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to prepare upload directory"})
		return
	}

	randPart := make([]byte, 8)
	if _, err := rand.Read(randPart); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate file name"})
		return
	}
	fileName := fmt.Sprintf("%d-%x%s", time.Now().Unix(), randPart, ext)
	savePath := filepath.Join(ownerDir, fileName)

	if err := c.SaveUploadedFile(file, savePath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to save image"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "image uploaded",
		"url":     "/uploads/" + sanitizePathSegment(ownerKey) + "/" + fileName,
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

func sanitizePathSegment(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "shared"
	}
	replacer := strings.NewReplacer("@", "_at_", ".", "_", "/", "_", "\\", "_", ":", "_", " ", "_")
	return replacer.Replace(value)
}

func (h *Handler) logAuthAttempt(c *gin.Context, event models.AuthEvent) {
	event.IP = c.ClientIP()
	event.ForwardedFor = strings.TrimSpace(c.GetHeader("X-Forwarded-For"))
	event.UserAgent = c.Request.UserAgent()
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	event.HostName = lookupHostName(event.IP)
	_ = h.DB.LogAuthEvent(event)
}

func lookupHostName(ip string) string {
	parsed := net.ParseIP(strings.TrimSpace(ip))
	if parsed == nil {
		return ""
	}
	names, err := net.LookupAddr(parsed.String())
	if err != nil || len(names) == 0 {
		return ""
	}
	return strings.TrimSuffix(names[0], ".")
}

func generateInviteTokenAndHash() (string, string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	return token, hashInviteToken(token), nil
}

func hashInviteToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return fmt.Sprintf("%x", sum)
}
