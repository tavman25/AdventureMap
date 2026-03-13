package database

import (
	"database/sql"
	"log"
	"strings"
	"time"

	"travel-map/internal/models"

	_ "modernc.org/sqlite"
)

// DB wraps the sql.DB connection.
type DB struct {
	conn *sql.DB
}

// New opens (or creates) the SQLite database and runs migrations.
func New(path string) (*DB, error) {
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db := &DB{conn: conn}
	if err := db.migrate(); err != nil {
		return nil, err
	}
	return db, nil
}

func (db *DB) migrate() error {
	_, err := db.conn.Exec(`
		CREATE TABLE IF NOT EXISTS pins (
			id          INTEGER PRIMARY KEY AUTOINCREMENT,
			title       TEXT    NOT NULL,
			description TEXT    NOT NULL DEFAULT '',
			image_url   TEXT    NOT NULL DEFAULT '',
			latitude    REAL    NOT NULL,
			longitude   REAL    NOT NULL,
			color       TEXT    NOT NULL DEFAULT '#FF5722',
			icon        TEXT    NOT NULL DEFAULT '📍',
			visited_at  TEXT    NOT NULL DEFAULT '',
			created_at  DATETIME NOT NULL,
			updated_at  DATETIME NOT NULL
		);
	`)
	if err != nil {
		log.Printf("migration error: %v", err)
		return err
	}

	// Backfill for databases created before image_url existed.
	_, err = db.conn.Exec(`ALTER TABLE pins ADD COLUMN image_url TEXT NOT NULL DEFAULT ''`)
	if err != nil && !strings.Contains(strings.ToLower(err.Error()), "duplicate column") {
		log.Printf("migration alter error: %v", err)
		return err
	}
	return nil
}

// GetAllPins returns all pins ordered by creation time descending.
func (db *DB) GetAllPins() ([]models.Pin, error) {
	rows, err := db.conn.Query(`
		SELECT id, title, description, image_url, latitude, longitude, color, icon, visited_at, created_at, updated_at
		FROM pins ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var pins []models.Pin
	for rows.Next() {
		var p models.Pin
		var ca, ua string
		if err := rows.Scan(&p.ID, &p.Title, &p.Description, &p.ImageURL, &p.Latitude, &p.Longitude,
			&p.Color, &p.Icon, &p.VisitedAt, &ca, &ua); err != nil {
			return nil, err
		}
		p.CreatedAt, _ = time.Parse(time.RFC3339, ca)
		p.UpdatedAt, _ = time.Parse(time.RFC3339, ua)
		pins = append(pins, p)
	}
	if pins == nil {
		pins = []models.Pin{}
	}
	return pins, nil
}

// GetPin returns a single pin by ID.
func (db *DB) GetPin(id int64) (*models.Pin, error) {
	row := db.conn.QueryRow(`
		SELECT id, title, description, image_url, latitude, longitude, color, icon, visited_at, created_at, updated_at
		FROM pins WHERE id = ?
	`, id)
	var p models.Pin
	var ca, ua string
	if err := row.Scan(&p.ID, &p.Title, &p.Description, &p.ImageURL, &p.Latitude, &p.Longitude,
		&p.Color, &p.Icon, &p.VisitedAt, &ca, &ua); err != nil {
		return nil, err
	}
	p.CreatedAt, _ = time.Parse(time.RFC3339, ca)
	p.UpdatedAt, _ = time.Parse(time.RFC3339, ua)
	return &p, nil
}

// CreatePin inserts a new pin and returns its assigned ID.
func (db *DB) CreatePin(p *models.Pin) error {
	now := time.Now().UTC()
	p.CreatedAt = now
	p.UpdatedAt = now
	result, err := db.conn.Exec(`
		INSERT INTO pins (title, description, image_url, latitude, longitude, color, icon, visited_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, p.Title, p.Description, p.ImageURL, p.Latitude, p.Longitude, p.Color, p.Icon, p.VisitedAt,
		now.Format(time.RFC3339), now.Format(time.RFC3339))
	if err != nil {
		return err
	}
	p.ID, err = result.LastInsertId()
	return err
}

// UpdatePin modifies an existing pin.
func (db *DB) UpdatePin(p *models.Pin) error {
	p.UpdatedAt = time.Now().UTC()
	_, err := db.conn.Exec(`
		UPDATE pins SET title=?, description=?, image_url=?, latitude=?, longitude=?, color=?, icon=?, visited_at=?, updated_at=?
		WHERE id=?
	`, p.Title, p.Description, p.ImageURL, p.Latitude, p.Longitude, p.Color, p.Icon, p.VisitedAt,
		p.UpdatedAt.Format(time.RFC3339), p.ID)
	return err
}

// DeletePin removes a pin by ID.
func (db *DB) DeletePin(id int64) error {
	_, err := db.conn.Exec(`DELETE FROM pins WHERE id = ?`, id)
	return err
}

// BulkCreatePins inserts multiple pins efficiently using a transaction.
func (db *DB) BulkCreatePins(pins []models.Pin) (int, error) {
	tx, err := db.conn.Begin()
	if err != nil {
		return 0, err
	}
	stmt, err := tx.Prepare(`
		INSERT INTO pins (title, description, image_url, latitude, longitude, color, icon, visited_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		tx.Rollback()
		return 0, err
	}
	defer stmt.Close()

	now := time.Now().UTC().Format(time.RFC3339)
	count := 0
	for _, p := range pins {
		_, err := stmt.Exec(p.Title, p.Description, p.ImageURL, p.Latitude, p.Longitude,
			p.Color, p.Icon, p.VisitedAt, now, now)
		if err != nil {
			tx.Rollback()
			return count, err
		}
		count++
	}
	return count, tx.Commit()
}

// UpdatePinImageByID sets a pin image URL by ID.
func (db *DB) UpdatePinImageByID(id int64, imageURL string) (bool, error) {
	result, err := db.conn.Exec(`UPDATE pins SET image_url=?, updated_at=? WHERE id=?`, imageURL, time.Now().UTC().Format(time.RFC3339), id)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}

// UpdatePinImageByTitle sets pin image URL for case-insensitive exact title matches.
func (db *DB) UpdatePinImageByTitle(title, imageURL string) (int64, error) {
	result, err := db.conn.Exec(
		`UPDATE pins SET image_url=?, updated_at=? WHERE lower(title)=lower(?)`,
		imageURL,
		time.Now().UTC().Format(time.RFC3339),
		title,
	)
	if err != nil {
		return 0, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	return affected, nil
}

// NormalizePinImageURLs repairs malformed stored image_url values, including concatenated URLs.
func (db *DB) NormalizePinImageURLs() (int64, error) {
	rows, err := db.conn.Query(`SELECT id, image_url FROM pins WHERE image_url <> ''`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	updated := int64(0)
	now := time.Now().UTC().Format(time.RFC3339)
	for rows.Next() {
		var id int64
		var raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return updated, err
		}
		normalized := normalizeStoredImageURLs(raw)
		if normalized == raw {
			continue
		}
		if _, err := db.conn.Exec(`UPDATE pins SET image_url=?, updated_at=? WHERE id=?`, normalized, now, id); err != nil {
			return updated, err
		}
		updated++
	}
	return updated, rows.Err()
}

func normalizeStoredImageURLs(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}

	parts := splitStoredImageURLs(trimmed)
	if len(parts) == 0 {
		parts = strings.FieldsFunc(trimmed, func(r rune) bool {
			return r == '\n' || r == '\r' || r == ',' || r == ' ' || r == '\t'
		})
	}

	seen := map[string]struct{}{}
	clean := make([]string, 0, len(parts))
	for _, part := range parts {
		candidate := strings.TrimSpace(part)
		if !strings.HasPrefix(strings.ToLower(candidate), "http://") && !strings.HasPrefix(strings.ToLower(candidate), "https://") {
			continue
		}
		if _, exists := seen[candidate]; exists {
			continue
		}
		seen[candidate] = struct{}{}
		clean = append(clean, candidate)
	}

	return strings.Join(clean, "\n")
}

func splitStoredImageURLs(raw string) []string {
	var parts []string
	remaining := raw
	for {
		start := indexOfProtocol(remaining)
		if start < 0 {
			break
		}
		remaining = remaining[start:]
		next := indexOfNextProtocol(remaining)
		if next < 0 {
			parts = append(parts, remaining)
			break
		}
		parts = append(parts, remaining[:next])
		remaining = remaining[next:]
	}
	return parts
}

func indexOfProtocol(s string) int {
	httpIndex := strings.Index(strings.ToLower(s), "http://")
	httpsIndex := strings.Index(strings.ToLower(s), "https://")
	if httpIndex < 0 {
		return httpsIndex
	}
	if httpsIndex < 0 {
		return httpIndex
	}
	if httpIndex < httpsIndex {
		return httpIndex
	}
	return httpsIndex
}

func indexOfNextProtocol(s string) int {
	if len(s) == 0 {
		return -1
	}
	httpIndex := strings.Index(strings.ToLower(s[1:]), "http://")
	httpsIndex := strings.Index(strings.ToLower(s[1:]), "https://")
	if httpIndex >= 0 {
		httpIndex++
	}
	if httpsIndex >= 0 {
		httpsIndex++
	}
	if httpIndex < 0 {
		return httpsIndex
	}
	if httpsIndex < 0 {
		return httpIndex
	}
	if httpIndex < httpsIndex {
		return httpIndex
	}
	return httpsIndex
}

// Close closes the database connection.
func (db *DB) Close() error {
	return db.conn.Close()
}
