package main

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
	"os"

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
	ginMode := getEnv("GIN_MODE", "release")

	gin.SetMode(ginMode)

	// Ensure data directory exists
	if err := os.MkdirAll("./data", 0755); err != nil {
		log.Fatalf("failed to create data directory: %v", err)
	}
	if err := os.MkdirAll("./data/uploads", 0755); err != nil {
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
	h := handlers.New(db)

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
		api.GET("/pins", h.GetPins)
		api.GET("/pins/:id", h.GetPin)
		api.POST("/pins", h.CreatePin)
		api.PUT("/pins/:id", h.UpdatePin)
		api.DELETE("/pins/:id", h.DeletePin)
		api.POST("/import/googlemaps", h.ImportGoogleMaps)
		api.POST("/import/photos", h.ImportPinPhotos)
		api.POST("/upload/image", h.UploadImage)
	}

	// Serve uploaded images from disk.
	r.Static("/uploads", "./data/uploads")

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
