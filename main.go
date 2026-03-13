package main

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"travel-map/internal/database"
	"travel-map/internal/handlers"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

//go:embed static
var staticFiles embed.FS

func main() {
	// Configuration from environment variables
	port := getEnv("PORT", "8080")
	dbPath := getEnv("DB_PATH", "./data/travel.db")
	uploadDir := getEnv("UPLOAD_DIR", filepath.Join(filepath.Dir(dbPath), "uploads"))
	adminPassword := getEnv("ADMIN_PASSWORD", "")
	authSecret := getEnv("AUTH_SECRET", "travel-map-auth-secret")
	googleClientID := getEnv("GOOGLE_CLIENT_ID", "")
	allowedEmails := getEnv("ALLOWED_EMAILS", "")
	ginMode := getEnv("GIN_MODE", "release")

	gin.SetMode(ginMode)

	// Ensure writable data directories exist.
	dataDir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		log.Fatalf("failed to create data directory: %v", err)
	}
	if err := os.MkdirAll(uploadDir, 0755); err != nil {
		log.Fatalf("failed to create uploads directory: %v", err)
	}

	// Initialize database
	db, err := database.New(dbPath)
	if err != nil {
		log.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()
	log.Printf("database ready at %s", dbPath)
	if repaired, err := db.NormalizePinImageURLs(); err != nil {
		log.Printf("image URL cleanup failed: %v", err)
	} else if repaired > 0 {
		log.Printf("repaired malformed image URLs on %d pin(s)", repaired)
	}

	// Initialize handlers
	h := handlers.New(db, uploadDir, adminPassword, authSecret, googleClientID, allowedEmails)

	// Set up router
	r := gin.New()
	r.Use(gin.Logger())
	r.Use(gin.Recovery())

	// CORS — allow all origins (adjust in production if needed)
	r.Use(cors.New(cors.Config{
		AllowAllOrigins:  true,
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept"},
	}))

	// REST API routes
	api := r.Group("/api")
	{
		api.GET("/auth/status", h.GetAuthStatus)
		api.POST("/auth/login", h.Login)
		api.POST("/auth/google", h.GoogleLogin)
		api.POST("/auth/logout", h.Logout)

		api.GET("/pins", h.GetPins)
		api.GET("/pins/:id", h.GetPin)
		api.GET("/profile", h.GetProfile)

		superAdmin := api.Group("")
		superAdmin.Use(h.RequireSuperAdmin())
		{
			superAdmin.GET("/IPCheck", h.GetIPCheck)
			superAdmin.GET("/auth/events", h.GetIPCheck)
		}

		admin := api.Group("")
		admin.Use(h.RequireAdmin())
		{
			admin.PUT("/profile", h.UpdateProfile)
			admin.GET("/clone/invites", h.ListCloneInvites)
			admin.POST("/clone/invites", h.CreateCloneInvite)
			admin.POST("/clone/invites/:id/revoke", h.RevokeCloneInvite)
			admin.GET("/clone/events", h.GetCloneEvents)
			admin.POST("/clone/accept", h.AcceptCloneInvite)
			admin.POST("/pins", h.CreatePin)
			admin.PUT("/pins/:id", h.UpdatePin)
			admin.DELETE("/pins/:id", h.DeletePin)
			admin.POST("/import/googlemaps", h.ImportGoogleMaps)
			admin.POST("/import/photos", h.ImportPinPhotos)
			admin.POST("/upload/image", h.UploadImage)
		}
	}

	// Serve uploaded images from disk.
	r.Static("/uploads", uploadDir)

	// Serve embedded static files (HTML, CSS, JS)
	stripped, err := fs.Sub(staticFiles, "static")
	if err != nil {
		log.Fatalf("failed to strip static prefix: %v", err)
	}
	r.NoRoute(func(c *gin.Context) {
		http.FileServer(http.FS(stripped)).ServeHTTP(c.Writer, c.Request)
	})

	log.Printf("travel map server starting on :%s", port)
	if err := r.Run(":" + port); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}
