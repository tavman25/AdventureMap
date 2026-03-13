package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"travel-map/internal/database"
	"travel-map/internal/handlers"

	"github.com/gin-gonic/gin"
)

func setupTestRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	tmpDB := t.TempDir() + "/test.db"
	uploadDir := t.TempDir() + "/uploads"
	db, err := database.New(tmpDB)
	if err != nil {
		t.Fatalf("failed to create test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	h := handlers.New(db, uploadDir, "", "test-secret", "", "")
	r := gin.New()
	api := r.Group("/api")
	api.GET("/pins", h.GetPins)
	api.POST("/pins", h.CreatePin)
	api.DELETE("/pins/:id", h.DeletePin)
	return r
}

func TestGetPinsEmpty(t *testing.T) {
	r := setupTestRouter(t)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, "/api/pins", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
}

func TestMain(m *testing.M) {
	os.Exit(m.Run())
}
